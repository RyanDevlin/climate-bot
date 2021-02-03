package ftpmachine

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RyanDevlin/planet-pulse/pkg/ferror"
	lru "github.com/hashicorp/golang-lru"
	"github.com/jlaffaye/ftp"
)

/***********************************************************************
This file acts as a wrapper around the jlaffaye/ftp library.
The purpose of this file is to provide simple methods to
interact with an FTP server, without the need to worry
about the intricacies of the jlaffaye/ftp library.
***********************************************************************/

// This limit is based on the number of ports available for outbound connections in Linux. Realistically one will never hit
// this limit as the number of outgoing connections will most likely be first limited by resources such as RAM, CPU, file descriptors,
// or other kernel limits. This is simply used as a max ceiling so one cannot attempt to create 2,000,000 connections. Also, since the synchronous
// connections in this package use goroutines, the overhead for managing them will probably be the biggest tax on the system before port limitations.
// Further, this limit must comply with the number of conncurrent connections the remote server is willing to accept from a single IP. Most FTP servers
// won't even accept more than 8 or so simultaneous connections from a single IP, so this theoretical max is really just for fun.
const connectionLimit = 65535

// FTPServer represents a remote FTP server
type FTPServer struct {
	Hostname           string
	Username           string
	Password           string
	Timeout            int        // This should be optional
	PendingConnections int32      // The unique ID of a new runtime connection. Is incremented each time we make a new connection. Needs to be initialized to 0 in order to properly flush pending connections.
	Connections        chan int32 // A buffer channel semaphore used to limite the number of connections we open concurrently to the remote server
	haltSearch         chan bool  // A channel to signal the program to stop searching the server
	cancelPending      chan bool  // A channel used to cancel all pending server connections
	attemptedConn      int32      // Incremented with each call to ftpSyncConnect()
}

type FTPConnection struct {
	c         *ftp.ServerConn
	cancelled bool  // The state of the pending connection
	connID    int32 // Unique ID used to track number of attempted connections
}

// Had to build my own version of this because jlaffaye/ftp doesn't include the path to the file :(
type FTPEntry struct {
	Name   string
	Path   string
	Target string // target of symbolic link
	Type   ftp.EntryType
	Size   uint64
	Time   time.Time
}

// FTPCache represents a local cache of FTP data
type FTPCache struct {
	path    string   // Local path where the cache will live
	name    string   // Name of the cache. This is what the dir under the above path will be named.
	filemap []string // A slice of file paths on the remote server
	cache   *lru.Cache
}

type CacheEntry struct {
	path      string
	timestamp time.Time
	data      string
}

type FTPMachine struct {
	Server FTPServer
	Cache  FTPCache
	// Should have methods to destroy existing cache maybe
}

// NewFTPMachine builds an FTPMachine struct representing a remote FTP server and a local cache of file data for that server.
// ==========================================================================================================================
// hostname: Any valid domain name representing the remote FTP server,
// username: The username to authenticate to the FTP server,
// password: The password to authenticate to the FTP server,
// maxConnections: The number of concurrent connections from a single IP that the remote FTP server will allow. This is usually fairly low, around 8.
// If you are receiving “421 Too many connections” errors from the server, reduce this value and try again.
func NewFTPMachine(hostname, username, password string, maxConnections int) (*FTPMachine, error) { // TODO: Add timeout parameter
	// Server parameters are validated here. It's possible to call lower level functions on their own, but this skips
	// the variable validation performed in NewFTPMachine(), which could result in unexpected behavior.

	if !isDomainName(hostname) {
		return nil, errors.New("NewFTPMachine: Supplied hostname '" + hostname + "' is not valid.")
	}

	if maxConnections > connectionLimit || maxConnections < 1 {
		return nil, errors.New("NewFTPMachine: maxConnections must be between 1 and " + fmt.Sprint(connectionLimit) + ".")
	}

	server := FTPServer{
		Hostname:           hostname,
		Username:           username,
		Password:           password,
		PendingConnections: 0,
		Connections:        make(chan int32, maxConnections),
		haltSearch:         make(chan bool),
		cancelPending:      make(chan bool),
		attemptedConn:      0,
	}

	ftpmachine := FTPMachine{
		Server: server,
		Cache:  FTPCache{}, // TODO: Add caching functionality
	}

	return &ftpmachine, nil
}

// This function returns data read from offset to EOF of a file on an FTP server.
// It automatically handles searching the server if path is en empty string or if the
// file is not found. This function will check all sub-paths under the given path.
// It will check the cache for file data or a path before searching. Returns error
// if file cannot be located.
func (server *FTPServer) Get(filename, path string, offset uint64) ([]byte, error) {

	ftpEntry, err := server.GetMeta(filename, path)
	if err != nil {
		return nil, err
	}

	data, err := server.GetFile(ftpEntry.Name, ftpEntry.Path, false, offset)
	if ferror.ErrorLog(err) {
		return nil, err
	}
	return data, nil

}

// Returns metadata of a file via FTPEntry struct.
// If file not found at the path given, this function
// will search for the file on all sub-paths.
func (server *FTPServer) GetMeta(filename, path string) (FTPEntry, error) {

	result := make(chan FTPEntry)
	sigError := make(chan error)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go server.search(filename, path, result, sigError, wg)

	// Wait for server to be fully searched
	wg.Wait()
	select {
	case ftpEntry := <-result:
		return ftpEntry, nil
	case err := <-sigError:
		return FTPEntry{}, err
	default:
		return FTPEntry{}, errors.New("Get: Error file '" + filename + "' not found on the server")
	}
}

// Returns data read from offset to end of a file
// Returns an error if the file was not found at the given path
func (server *FTPServer) GetFile(filename string, path string, cancelable bool, offset uint64) ([]byte, error) {

	// Establish FTP connection
	conn, err := server.ftpSyncConnect(cancelable)
	defer server.ftpDisconnect(conn)
	if err != nil {
		return nil, err
	}

	// Using ChangeDir as "stat" function
	result := conn.c.ChangeDir(path)
	if result != nil {
		return nil, errors.New("ftpList: Error '" + path + "' does not exist on the server")
		//return nil, errors.New("DNE")
	}

	// Because ftpSyncConnect() only returns after a connection has been established or cancelled, we can check this to throw out the request if it was cancelled
	if conn.cancelled {
		return nil, nil
	}

	// Pull down the file
	r, err := conn.c.RetrFrom(filename, offset)
	if err != nil {
		return nil, err
	}

	buf, err := ioutil.ReadAll(r)
	return buf, nil
}

// Given a filename, this function searches all sub-paths at the
// provided path for the file. It returns the path to the file if found.
// If no path is provided, this searches all sub-paths from the root of the server.
// If no path is found this returns an error.
func (server *FTPServer) search(filename, path string, result chan FTPEntry, sigError chan error, wg *sync.WaitGroup) {
	// TODO: Add error logging and handling. If file not found,
	// should log that. If verbosity is on, list all paths searched..
	path = filepath.Join("/", path)

	select {
	case <-server.haltSearch: // If one of the goroutines found the file, the halt channel will close which will unblock the case and immediately halt the search
		wg.Done()
		return
	default:
		fmt.Println("Searching remote location:", "ftp://"+filepath.Join(server.Hostname, path))
		list, err := server.ftpList(path, true)
		if err != nil {
			wg.Done()
			sigError <- err // TODO: Proper error handling
			return
		}

		for _, entry := range list {
			if entry.Name == filename {

				ftpEntry := FTPEntry{
					Name:   entry.Name,
					Path:   path,
					Target: entry.Target,
					Type:   entry.Type,
					Size:   entry.Size,
					Time:   entry.Time,
				}
				fmt.Println("==== FOUND IT: ", ftpEntry.Path)
				pending := int(atomic.LoadInt32(&server.PendingConnections))
				for i := 0; i < pending; i++ {
					server.cancelPending <- true // Flushes all queued connections
				}
				close(server.haltSearch) // Stop searching
				wg.Done()
				result <- ftpEntry
				return
			}
			if entry.Type == ftp.EntryTypeFolder {
				subPath := filepath.Join(path, entry.Name)
				wg.Add(1)
				go server.search(filename, subPath, result, sigError, wg)
			}
		}
	}

	wg.Done()
	return
}

func (server *FTPServer) ftpList(path string, cancelable bool) ([]*ftp.Entry, error) {
	// Establish FTP connection
	conn, err := server.ftpSyncConnect(cancelable)
	defer server.ftpDisconnect(conn)
	if err != nil {
		return nil, err
	}

	if conn.cancelled {
		return nil, nil
	}

	// Because of the way jlaffaye/ftp was written, the List() function will not tell us if the path exists or not
	// Instead we have to use ChangeDir to first test this, which is dumb
	result := conn.c.ChangeDir(path)
	if result != nil {
		return nil, errors.New("ftpList: Error '" + path + "' does not exist on the server")
	}

	list, err := conn.c.List(path)
	if err != nil {
		return nil, err
	}

	return list, err
}

func (server *FTPServer) ftpSyncConnect(cancelable bool) (*FTPConnection, error) {
	// Increment the number of connection attempts
	currID := atomic.AddInt32(&server.attemptedConn, 1)

	connection := &FTPConnection{
		c:         nil,
		cancelled: false,
		connID:    currID, // Used to mark this connection attempt with a unique ID
	}

	if cancelable { // This loop allows pending connection requests to be aborted by sending to the server.CancelPending channel
		// Increment pending counter only for cancelable requests
		atomic.AddInt32(&server.PendingConnections, 1)
		for {
			select {
			case <-server.cancelPending: // Sends to this channel flush pending connection requests
				atomic.AddInt32(&server.PendingConnections, -1)
				connection.cancelled = true
				return connection, nil
			case server.Connections <- int32(connection.connID): // Buffered channel semaphore to limit the number of concurrent connections
				atomic.AddInt32(&server.PendingConnections, -1)
				conn, err := server.ftpConnect(connection)
				connection.c = conn
				return connection, err
			}
		}
	}

	// This request cannot be aborted
	server.Connections <- int32(connection.connID)
	conn, err := server.ftpConnect(connection)
	connection.c = conn
	return connection, err
}

func (server *FTPServer) ftpConnect(connection *FTPConnection) (*ftp.ServerConn, error) {

	dialAddr := net.JoinHostPort(server.Hostname, "21") // jlaffaye/ftp requires the port. TODO: maybe add a port override, although that detracts from the simplicity goal of this package.
	c, err := ftp.Dial(dialAddr, ftp.DialWithTimeout(10*time.Second))
	if err != nil { // TODO: integrate with error handling
		log.Fatal(err)
	}
	fmt.Println("New connection established for connection #", connection.connID)

	// Log in to the FTP server
	err = c.Login(server.Username, server.Password)
	if err != nil {
		log.Fatal(err) // TODO: integrate with error handling
	}
	return c, nil
}

func (server *FTPServer) ftpDisconnect(connection *FTPConnection) {
	if !connection.cancelled {
		id := <-server.Connections // Release connection we are done with
		connection.c.Quit()
		fmt.Println("Completed disconnection for connection #", id)
		return
	}
	fmt.Println("Cancelled connection request for connection #", connection.connID)
}

// This beautiful function was completely stolen from golang.org/src/net/dnsclient.go
// Thank you golang team for your magical open source code :)

func isDomainName(s string) bool {
	// See RFC 1035, RFC 3696.
	// Presentation format has dots before every label except the first, and the
	// terminal empty label is optional here because we assume fully-qualified
	// (absolute) input. We must therefore reserve space for the first and last
	// labels' length octets in wire format, where they are necessary and the
	// maximum total length is 255.
	// So our _effective_ maximum is 253, but 254 is not rejected if the last
	// character is a dot.1
	l := len(s)
	if l == 0 || l > 254 || l == 254 && s[l-1] != '.' {
		return false
	}

	last := byte('.')
	nonNumeric := false // true once we've seen a letter or hyphen
	partlen := 0
	for i := 0; i < len(s); i++ {

		c := s[i]
		switch {
		default:
			return false
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_':
			nonNumeric = true
			partlen++
		case '0' <= c && c <= '9':
			// fine
			partlen++
		case c == '-':
			// Byte before dash cannot be dot.
			if last == '.' {
				return false
			}

			partlen++
			nonNumeric = true
		case c == '.':
			// Byte before dot cannot be dot, dash.
			if last == '.' || last == '-' {
				return false
			}

			if partlen > 63 || partlen == 0 {
				return false
			}
			partlen = 0
		}
		last = c
	}

	if last == '-' || partlen > 63 {
		return false
	}

	return nonNumeric
}
