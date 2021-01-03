package main

import (
	"fmt"
	"log"
	"os/user"

	"github.com/RyanDevlin/planet-pulse/internal"
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

func main() {
	// Grab the current user
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	cachedir := usr.HomeDir + "/" + cachename

	cache, err := internal.CreateCache(cachedir, sitemap)
	if err != nil {
		log.Fatal(err)
	}

	result, err := cache.FtpGetTimestamp("co2_weekly_mlo.csv")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("TIMESTAMP:", result)
	/*remote_buf := internal.Quickdial_ftp(ftp_default_server, Weekly_co2_mlo_path)
	data, err := ioutil.ReadFile(weekly_file)
	if err != nil {
		fmt.Println("File reading error", err)
		return
	}
	fmt.Println("Contents of file:", string(data))
	*/

	//fmt.Println(string(remote_buf[0]))
	//create_cache()
}
