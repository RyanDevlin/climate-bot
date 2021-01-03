package internal

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

const (
	// This is the name of the cache that will be created in the user's home dir
	cachename = ".planetpulsecache/"

	// These are variables specific to the NOAA FTP server
	ftp_default_server = "aftp.cmdl.noaa.gov"
	ftpuser            = "anonymous"
	ftppass            = "anonymous"
)

// sitemap is a mapping of data file names to their location on the NOAA FTP server
var sitemap = map[string]string{
	"co2_weekly_mlo.txt": "products/trends/co2/",
	"co2_weekly_mlo.csv": "products/trends/co2/",
}

// FTPDataCache represents a cache for data pulled from an FTP server
type FTPDataCache struct {
	path       string
	files      map[string]string // A map of local file names to remote paths
	remotehost string
	remoteuser string
	remotepass string

	// Server capabilities discovered at runtime
	features      map[string]string
	skipEPSV      bool
	mlstSupported bool
	usePRET       bool
}

// ERRORS
type FTPError struct {
	What string
}

func (e *FTPError) Error() string {
	return fmt.Sprintf("%s", e.What)
}

// ========================= MAIN ========================= //
func main() {
	// Grab the current user
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	cachedir := usr.HomeDir + "/" + cachename

	cache, err := CreateCache(cachedir, sitemap)
	if err != nil {
		log.Fatal(err)
	}
	cache.FtpGetTimestamp("co2_weekly_mlo.csv")

	//remote_buf := Quickdial_ftp(ftp_default_server, Weekly_co2_mlo_path)
	/*data, err := ioutil.ReadFile(weekly_file)
	if err != nil {
		fmt.Println("File reading error", err)
		return
	}
	fmt.Println("Contents of file:", string(data))*/

	//fmt.Println(string(remote_buf[0]))
	//create_cache()
}

/***********************************************************************
Quickdial_ftp connencts to an anonymous FTP server and retrieves files.
Files are returned as a byte array.

server = a string for an FTP server
file_path = a path to the file starting from the base domain
(eg. products/trends/co2/co2_weekly_mlo.txt)
***********************************************************************/
func Quickdial_ftp(server, file_path string) []byte {

	// This FTP library requires the port appended
	dial_addr := server + ":21"
	c, err := ftp.Dial(dial_addr)
	if err != nil {
		log.Fatal(err)
	}

	// Log in anonymously
	err = c.Login("anonymous", "anonymous")
	if err != nil {
		log.Fatal(err)
	}

	found, err := c.List("products/trends/co2/")

	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(found[0].Time)

	// Pull down the file
	r, err := c.Retr(file_path)
	if err != nil {
		panic(err)
	}
	defer r.Close()

	buf, err := ioutil.ReadAll(r)
	//buf := []byte{4}
	return buf
}

// Removes a header from a byte array
// Assumes each header line starts with '#'
func remove_header() {

}

func CreateCache(path string, sitemap map[string]string) (*FTPDataCache, error) {

	// MkdirAll will create this path if it doesn't exist
	err := os.MkdirAll(path, 0755)
	if err != nil {
		// We need the cache to run this application
		return nil, err
	}

	cache := &FTPDataCache{
		path:       path,
		files:      sitemap,
		remotehost: ftp_default_server,
		remoteuser: ftpuser,
		remotepass: ftppass,
	}
	return cache, nil
}

func (cache *FTPDataCache) Probe(filename string) (*[]byte, error) {
	// Compare cache to FTP server
	cache.Look(filename)
	return nil, nil
}

// Checks if data in the cache matches data on the FTP server
func (cache *FTPDataCache) Look(filename string) (bool, error) {

	return false, nil
}

func (cache *FTPDataCache) FtpGetTimestamp(filename string) (time.Time, error) {
	timestamp, err := cache.timestamp(filename)
	if ErrorLog(err) {
		var zeroVal time.Time
		return zeroVal, err
	}
	return timestamp, nil
}

// Do not call directly, graceful error handling in FtpGetTimestamp()
func (cache *FTPDataCache) timestamp(filename string) (time.Time, error) {
	var zeroVal time.Time
	// Establish FTP connection
	c := ftpConnect(cache.remotehost, cache.remoteuser, cache.remotepass)

	// Issue LIST command to get remote directory information
	dir, ok := cache.files[filename]
	if !ok {
		err := fmt.Errorf("file '" + filename + "' is not in the sitemap.")
		return zeroVal, err
	}

	found, err := c.List(dir)
	if err != nil {
		return zeroVal, err
	}

	for _, entry := range found {
		if entry.Name == filename {
			return entry.Time, nil
		}
	}

	err = fmt.Errorf("file '" + filename + "' not found on server ftp://" + cache.remotehost + "/" + dir)
	return zeroVal, err
}

func (cache *FTPDataCache) ftpFetch(server, file_path string) []byte {
	// Establish FTP connection
	c := ftpConnect(server, cache.remoteuser, cache.remotepass)

	// Pull down the file
	r, err := c.Retr(file_path)
	if err != nil {
		panic(err)
	}
	defer r.Close()

	buf, err := ioutil.ReadAll(r)
	return buf
}

func ftpConnect(server, username, password string) *ftp.ServerConn {
	// This FTP library requires the port appended
	dial_addr := server + ":21"
	c, err := ftp.Dial(dial_addr)
	if err != nil {
		log.Fatal(err)
	}

	// Log in anonymously
	err = c.Login(username, password)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

/*
func ftpConnect(server, username, password string) *ftp.ServerConn {
}
*/

func ErrorLog(err error) (b bool) {
	if err != nil {
		pc, fn, line, _ := runtime.Caller(1)
		slice := strings.Split(fn, "/")
		log.Printf("[ERROR] in %s[%s:%d]: %v", runtime.FuncForPC(pc).Name(), slice[len(slice)-1], line, err)
		b = true
	}
	return
}
