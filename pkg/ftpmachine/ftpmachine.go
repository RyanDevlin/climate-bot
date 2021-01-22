package ftpmachine

import (
	"fmt"
	"io/ioutil"
	"log"
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

// FTPServer represents a remote FTP server
type FTPServer struct {
	Hostname    string
	Username    string
	Password    string
	Timeout     int // This should be optional
	Connections int32
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

// This function pulls down a file from an FTP server. It automatically
// handles searching the server if path is nil or if the file is not found
// this function will check all sub-paths under the given path. It will check the cache
// for file data or a path before searching. Returns error if file cannot be located.
func (server *FTPServer) Get(filename string, path string) ([]byte, error) {
	// TODO: make "path" optional
	// TODO: dir list at path or root first, if filename is there, get it.
	// otherwise, call the search function
	data, err := server.getFile(filename, path)
	if ferror.ErrorLog(err) {

		ch := make(chan string)
		var wg sync.WaitGroup
		wg.Add(1)
		go server.Search(filename, path, ch, wg)

		truePath := <-ch
		fmt.Println("TRUEPATH: ", truePath)
		data, err := server.getFile(filename, truePath)
		if ferror.ErrorLog(err) {
			return nil, err
		}
		return data, nil
	}

	//server.Search(filename, path)
	return data, nil
}

// Given a filename, this function searches all sub-paths at the
// provided path for the file. It returns the path to the file if found.
// If no path is provided, this searches all sub-paths from the root of the server.
// If no path is found this returns an error.
func (server *FTPServer) Search(filename string, path string, ch chan string, wg sync.WaitGroup) {
	// TODO: filename and path validation. For path validation try filepath.Clean
	// TODO: Add error logging and handling. If file not found,
	// should log that. If verbosity is on, list all paths searched..
	// TODO: Can make my own traversal function that uses go routines
	// In theory should be faster than the library.
	// TODO: Since the jlaffaye/ftp library connections don't support
	// concurrency, we will need to open many of them to multithread the
	// searching algorithm. One thing to watch out for here is that we
	// will be spinning up a lot of go routines to search each dir,
	// depending on the number of sub-dirs on the remote server. One
	// mitigating factor might be that the remote connections should
	// immediately end after obtaining their map. We should ensure this
	// function DOES NOT hold the connection open while it does other processing.

	defer wg.Done()
	fmt.Println("SEARCHING: ", path)
	list, err := server.ftpList(path)
	if err != nil {
		log.Fatal("UH OH")
	}

	for _, entry := range list {
		if entry.Name == filename {
			ch <- filepath.Join(path)
			return
		}
		if entry.Type == ftp.EntryTypeFolder {
			subPath := filepath.Join(path, entry.Name)
			//wg.Add(int(atomic.LoadInt32(&server.Connections)))
			wg.Add(1)
			go server.Search(filename, subPath, ch, wg)
		}
	}

	//fmt.Println("FOUND: ", list[0].Type)

	return
}

func (server *FTPServer) ftpList(path string) ([]*ftp.Entry, error) {
	// Establish FTP connection
	c := server.ftpConnect()
	defer server.ftpDisconnect(c)

	list, err := c.List(path)
	if err != nil {
		return nil, err
	}

	return list, err
}

func (server *FTPServer) GetTimestamp(filename string, path string) (time.Time, error) {
	timestamp, err := server.ftpTimestamp(filename, path)
	if ferror.ErrorLog(err) {
		var zeroVal time.Time
		return zeroVal, err
	}
	return timestamp, nil
}

// Do not call directly, graceful error handling in GetTimestamp()
//
// Returns timestamp of given file. If no path provided, it discovers path with cache or Search.
func (server *FTPServer) ftpTimestamp(filename string, path string) (time.Time, error) {
	var zeroVal time.Time
	// Establish FTP connection
	c := server.ftpConnect()

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
// Logs errors to STDERR
func (server *FTPServer) getFile(filename string, path string) ([]byte, error) {
	// Build full path
	filePath := filepath.Join(path, filename)

	// Establish FTP connection
	c := server.ftpConnect() // TODO: Server connections with the jlaffaye/ftp library do not support concurrency

	// Pull down the file
	r, err := c.Retr(filePath)
	if err != nil {
		return nil, err
	}

	defer server.ftpDisconnect(c)

	buf, err := ioutil.ReadAll(r)
	return buf, nil
}

func (server *FTPServer) ftpConnect() *ftp.ServerConn {
	// TODO: Validate hostname
	// This FTP library requires the port appended

	atomic.AddInt32(&server.Connections, 1)
	fmt.Println("Connection: ", server.Connections)

	dialAddr := server.Hostname + ":21"
	c, err := ftp.Dial(dialAddr)
	if err != nil { // TODO: integrate with error handling

		log.Fatal(err)
	}

	// Log in anonymously
	err = c.Login(server.Username, server.Password)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

func (server *FTPServer) ftpDisconnect(c *ftp.ServerConn) {
	atomic.AddInt32(&server.Connections, -1)
	fmt.Println("Disconnection: ", server.Connections)
	c.Quit()
}
