package main

import (
	log "github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var Db *gorm.DB

func init() {
	Db, _ = gorm.Open(sqlite.Open(`C:\DEV\go-api\server\db\new_DEV.db`), &gorm.Config{})
}

func main() {
	urlC := GetCollector()
	dataC := GetCollector()
	urls := GetVCTmatches(urlC)
	furls := filterUrls(urls)
	dchan := GetCleanDataChan(furls, dataC)
	for ds := range dchan {
		if err := CreateDbEntries(ds, Db); err != nil {
			log.Info(err)
		}
	}

}
