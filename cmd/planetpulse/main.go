package main

import (
	//data "github.com/RyanDevlin/planet-pulse/internal/data"

	"fmt"

	"github.com/RyanDevlin/planet-pulse/pkg/ftpmachine"
)

const MaxConnections = 5

func main() {
	server := ftpmachine.FTPServer{
		Hostname:      "aftp.cmdl.noaa.gov",
		Username:      "anonymous",
		Password:      "anonymous",
		ConnectionID:  0,
		Connections:   make(chan int32, MaxConnections),
		HaltSearch:    make(chan bool),
		CancelPending: make(chan bool),
	}

	ftpmachine := ftpmachine.FTPMachine{
		Server: server,
		Cache:  ftpmachine.FTPCache{},
	}

	data, err := ftpmachine.Server.Get("co2_weekly_mlo.txt", "/products/")
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(string(data[0]))
}

/*

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

func main() {
	// Grab the current user
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	cachedir := usr.HomeDir + "/" + cachename

	cache, err := data.CreateCache(cachedir, sitemap)
	if err != nil {
		log.Fatal(err)
	}

	result, err := cache.FtpGetTimestamp("co2_weekly_mlo.csv")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("TIMESTAMP:", result)
//	remote_buf := data.Quickdial_ftp(ftp_default_server, Weekly_co2_mlo_path)
//	data, err := ioutil.ReadFile(weekly_file)
//	if err != nil {
//		fmt.Println("File reading error", err)
//		return
	//}
	//fmt.Println("Contents of file:", string(data))


	//fmt.Println(string(remote_buf[0]))
	//create_cache()
}
*/
