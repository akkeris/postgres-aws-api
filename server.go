package main

import (
	"database/sql"
	"fmt"
	"os"
	"log"
	"io/ioutil"
	"net/url"
	"github.com/go-martini/martini"
	_ "github.com/lib/pq"
	"github.com/martini-contrib/render"
	"./v1"
	"./v2"
)

func getDB(uri string) *sql.DB {
	db, dberr := sql.Open("postgres", uri)
	if dberr != nil {
		fmt.Println(dberr)
		return nil
	}
	// not available in 1.5 golang, youll want to turn it on for v1.6 or higher once upgraded.
	//pool.SetConnMaxLifetime(time.ParseDuration("1h"));
	db.SetMaxIdleConns(4)
	db.SetMaxOpenConns(20)
	return db
}


func main() {
	brokerdb := os.Getenv("BROKERDB")
	hobbydb := os.Getenv("HOBBYDB")

	pool := getDB(brokerdb)
	pool_hobby := getDB(hobbydb)

	// setup the database (or modify it as necessary)
	buf, err := ioutil.ReadFile("create.sql")
	if err != nil {
		log.Fatalf("Unable to read create.sql: %s\n", err)
	}
	_, err = pool.Query(string(buf))
	if err != nil {
		log.Fatal("Unable to create database: %s\n", err)
	}

	uri, err := url.Parse(hobbydb)
	if err != nil {
		log.Fatal("Failed to parse shared tenant database: %s\n", err)
	}
	if(uri.User != nil) {
		user := uri.User.Username()
		pass, is_set := uri.User.Password()
		if is_set == false {
			pass = ""
		}
		_, err = pool.Query("insert into shared_tenant (host, masteruser, masterpass) values ($1, $2, $3) on conflict (host) do update set (masteruser, masterpass) = ($2, $3) where shared_tenant.host=$1;", 
			uri.Host, user, pass)
		if err != nil {
			log.Fatal("Failed to insert/update shared tenant records: %s\n", err)
		}
	}

	m := martini.Classic()
	m.Use(render.Renderer())

	v1.Start(m, pool, pool_hobby)
	v2.Start(m, pool, pool_hobby)
	m.Run()
}

