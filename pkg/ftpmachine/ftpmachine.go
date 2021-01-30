package ftpmachine

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"path/filepath"
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
	Hostname      string
	Username      string
	Password      string
	Timeout       int        // This should be optional
	ConnectionID  int32      // The unique ID of a new runtime connection. Is incremented each time we make a new connection. Needs to be initialized to 0 in order to properly flush pending connections.
	Connections   chan int32 // A buffer channel semaphore used to limite the number of connections we open concurrently to the remote server
	haltSearch    chan bool  // A channel to signal the program to stop searching the server
	cancelPending chan bool  // A channel used to cancel all pending server connections
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
		Hostname:      hostname,
		Username:      username,
		Password:      password,
		ConnectionID:  0,
		Connections:   make(chan int32, maxConnections),
		haltSearch:    make(chan bool),
		cancelPending: make(chan bool),
	}

	ftpmachine := FTPMachine{
		Server: server,
		Cache:  FTPCache{}, // TODO: Add caching functionality
	}

	return &ftpmachine, nil
}

// This function pulls down a file from an FTP server. It automatically
// handles searching the server if path is nil or if the file is not found.
// This function will check all sub-paths under the given path. It will check the cache
// for file data or a path before searching. Returns error if file cannot be located.
func (server *FTPServer) Get(filename string, path string) ([]byte, error) {
	// TODO: make "path" optional
	// TODO: dir list at path or root first, if filename is there, get it.
	// otherwise, call the search function
	data, err := server.getFile(filename, path, false)
	if ferror.ErrorLog(err) {

		result := make(chan string)
		sigError := make(chan error)
		go server.Search(filename, path, result, sigError)

		select {
		case truePath := <-result:
			fmt.Println("TRUEPATH: ", truePath)
			data, err := server.getFile(filename, truePath, false)
			if ferror.ErrorLog(err) {
				return nil, err
			}
			return data, nil
		case err := <-sigError:
			return nil, err
		}
	}

	return data, nil
}

// Given a filename, this function searches all sub-paths at the
// provided path for the file. It returns the path to the file if found.
// If no path is provided, this searches all sub-paths from the root of the server.
// If no path is found this returns an error.
func (server *FTPServer) Search(filename string, path string, result chan string, sigError chan error) {
	// TODO: Add error logging and handling. If file not found,
	// should log that. If verbosity is on, list all paths searched..

	select {
	case <-server.haltSearch: // If one of the goroutines found the file, the halt channel will close which will unblock the case and immediately halt the search
		return
	default:
		fmt.Println("Searching remote location:", "ftp://"+filepath.Join(server.Hostname, path))
		list, err := server.ftpList(path, true)
		if err != nil {
			ferror.InfoLog(err.Error()) // TODO: Proper error handling
		}

		// This will break a deadlock if the path supplied doesn't exist on the server. If there is an empty dir on the server though this will explode.
		if len(list) == 0 {
			sigError <- errors.New("Search: Error supplied path " + path + " not found on " + server.Hostname)
			return
		}

		for _, entry := range list {
			if entry.Name == filename {
				fmt.Println("==== FOUND IT: ", filepath.Join(path))
				result <- filepath.Join(path)
				close(server.haltSearch) // Stop searching
				for i := 0; i < int(atomic.LoadInt32(&server.ConnectionID))-1; i++ {
					server.cancelPending <- true // Flushes all queued connections
				}
				return
			}
			if entry.Type == ftp.EntryTypeFolder {
				subPath := filepath.Join(path, entry.Name)
				go server.Search(filename, subPath, result, sigError)
			}
		}
	}

	// We get deadlock if the file isn't on the server! I need to do something more clever here. Maybe use wait groups or something. Clearly a lot of edge cases still need to be addressed.

	return
}

func (server *FTPServer) ftpList(path string, cancelable bool) ([]*ftp.Entry, error) {
	// Establish FTP connection
	c, err := server.ftpSyncConnect(cancelable)
	if err != nil {

		return nil, err
	}

	defer server.ftpDisconnect(c)

	list, err := c.List(path)
	if err != nil {
		return nil, err
	}

	return list, err
}

func (server *FTPServer) GetTimestamp(filename string, path string, cancelable bool) (time.Time, error) {
	timestamp, err := server.ftpTimestamp(filename, path, cancelable)
	if ferror.ErrorLog(err) {
		var zeroVal time.Time
		return zeroVal, err
	}
	return timestamp, nil
}

// Do not call directly, graceful error handling in GetTimestamp()
//
// Returns timestamp of given file. If no path provided, it discovers path with cache or Search.
func (server *FTPServer) ftpTimestamp(filename string, path string, cancelable bool) (time.Time, error) {
	var zeroVal time.Time
	// Establish FTP connection
	c, err := server.ftpSyncConnect(cancelable)
	if err != nil {
		return zeroVal, err
	}

	dir := "products/trends/co2/" // TODO: Fix this obviously

	found, err := c.List(dir)
	if err != nil {
		return zeroVal, err
	}

	for _, entry := range found {
		if entry.Name == filename {
			return entry.Time, nil
		}
	}

	err = fmt.Errorf("file '" + filename + "' not found on server ftp://" + server.Hostname + "/" + dir) // TODO: Make the slash handling much better by using filepath library
	return zeroVal, err
}

// Returns an error if the file was not found at the given path
func (server *FTPServer) getFile(filename string, path string, cancelable bool) ([]byte, error) {
	// Build full path
	filePath := filepath.Join(path, filename)

	// Establish FTP connection
	c, err := server.ftpSyncConnect(cancelable)
	//defer server.ftpDisconnect(c)
	if err != nil {
		return nil, err
	}

	// Pull down the file
	r, err := c.Retr(filePath)
	if err != nil {
		return nil, err
	}

	// TODO: KNOWN BUG - If the path given to this function does not exist, the connection will hang forever. Maybe need to specify a timeout here.
	// TODO: Determine if this defer should be here or higher up in the function
	defer server.ftpDisconnect(c)

	buf, err := ioutil.ReadAll(r)
	return buf, nil
}

func (server *FTPServer) ftpSyncConnect(cancelable bool) (*ftp.ServerConn, error) {
	// Increment the number of current connections
	id := atomic.AddInt32(&server.ConnectionID, 1)

	if cancelable { // This loop allows pending connection requests to be aborted by sending to the server.CancelPending channel
		for {
			select {
			case <-server.cancelPending: // If one of the goroutines found the file, the halt channel will close which will unblock the case and immediately halt the search
				return nil, errors.New("Cancelled connection request for job #" + fmt.Sprint(id))
			case server.Connections <- id: // Buffered channel semaphore to limit the number of concurrent connections
				conn, err := server.ftpConnect(id)
				return conn, err
			}
		}
	}

	// This request cannot be aborted
	server.Connections <- id
	conn, err := server.ftpConnect(id)
	return conn, err
}

func (server *FTPServer) ftpConnect(connID int32) (*ftp.ServerConn, error) {

	dialAddr := net.JoinHostPort(server.Hostname, "21") // jlaffaye/ftp requires the port. TODO: maybe add a port override, although that detracts from the simplicity goal of this package.
	c, err := ftp.Dial(dialAddr, ftp.DialWithTimeout(10*time.Second))
	if err != nil { // TODO: integrate with error handling
		log.Fatal(err)
	}
	fmt.Println("New connection established for job #", connID)

	// Log in anonymously
	err = c.Login(server.Username, server.Password)
	if err != nil {
		log.Fatal(err) // TODO: integrate with error handling
	}
	return c, nil
}

func (server *FTPServer) ftpDisconnect(c *ftp.ServerConn) {
	id := <-server.Connections // Release connection we are done with
	c.Quit()
	fmt.Println("Completed disconnection for job #", id)
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
