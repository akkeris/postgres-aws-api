package v1

import (
	"database/sql"
	"fmt"
	"os"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/go-martini/martini"
	_ "github.com/lib/pq"
	"github.com/martini-contrib/binding"
	"github.com/martini-contrib/render"
)

var pool *sql.DB
var pool_hobby *sql.DB

func Start(m *martini.ClassicMartini, provision_db *sql.DB, hobby_db *sql.DB) {
	pool = provision_db;
	pool_hobby = hobby_db;
	
	m.Post("/v1/postgres/instance", binding.Json(provisionspec{}), provision) // deprecated
	m.Delete("/v1/postgres/instance/:name", delete) // deprecated
	m.Get("/v1/postgres/url/:name", url) // deprecated
	m.Post("/v1/tag", binding.Json(tagspec{}), tag) // deprecated
	m.Get("/v1/postgres/plans", plans) // deprecated
}

func tag(spec tagspec, berr binding.Errors, r render.Render) {
	if berr != nil {
		fmt.Println(berr)
		errorout := make(map[string]interface{})
		errorout["error"] = berr
		r.JSON(500, errorout)
		return
	}
	svc := rds.New(session.New(&aws.Config{
		Region: aws.String(os.Getenv("REGION")),
	}))
	region := os.Getenv("REGION")
	accountnumber := os.Getenv("ACCOUNTNUMBER")
	name := spec.Resource

	arnname := "arn:aws:rds:" + region + ":" + accountnumber + ":db:" + name

	params := &rds.AddTagsToResourceInput{
		ResourceName: aws.String(arnname),
		Tags: []*rds.Tag{ // Required
			{
				Key:   aws.String(spec.Name),
				Value: aws.String(spec.Value),
			},
		},
	}
	resp, err := svc.AddTagsToResource(params)

	if err != nil {
		fmt.Println(err.Error())
		errorout := make(map[string]interface{})
		errorout["error"] = berr
		r.JSON(500, errorout)
		return
	}

	fmt.Println(resp)
	r.JSON(200, map[string]interface{}{"response": "tag added"})
}


func provision(spec provisionspec, err binding.Errors, r render.Render) {
	plan := spec.Plan
	billingcode := spec.Billingcode

	var name string
	dberr := pool.QueryRow("select name from provision where plan='" + plan + "' and claimed='no' and make_date=(select min(make_date) from provision where plan='" + plan + "' and claimed='no')").Scan(&name)
	if dberr != nil {
		fmt.Println(dberr)
		toreturn := dberr.Error()
		r.JSON(500, map[string]interface{}{"error": toreturn})
		return
	}
	fmt.Println(name)

	instanceName := name
	if spec.Plan == "micro" {
		instanceName = os.Getenv("HOBBY_INSTANCE_NAME")
	}

	available := isAvailable(instanceName)
	if available {
		var dbinfo dbspec
		stmt, dberr := pool.Prepare("update provision set claimed=$1 where name=$2")

		if dberr != nil {
			fmt.Println(dberr)
			toreturn := dberr.Error()
			r.JSON(500, map[string]interface{}{"error": toreturn})
			return
		}
		_, dberr = stmt.Exec("yes", name)
		if dberr != nil {
			fmt.Println(dberr)
			toreturn := dberr.Error()
			r.JSON(500, map[string]interface{}{"error": toreturn})
			return
		}

		if spec.Plan != "micro" {
			region := os.Getenv("REGION")
			svc := rds.New(session.New(&aws.Config{
				Region: aws.String(region),
			}))
			accountnumber := os.Getenv("ACCOUNTNUMBER")
			arnname := "arn:aws:rds:" + region + ":" + accountnumber + ":db:" + name

			params := &rds.AddTagsToResourceInput{
				ResourceName: aws.String(arnname),
				Tags: []*rds.Tag{ // Required
					{
						Key:   aws.String("billingcode"),
						Value: aws.String(billingcode),
					},
				},
			}

			resp, awserr := svc.AddTagsToResource(params)
			if awserr != nil {
				fmt.Println(awserr.Error())
				toreturn := awserr.Error()
				r.JSON(500, map[string]interface{}{"error": toreturn})
				return
			}
			fmt.Println(resp)
		}

		dbinfo, err := getDBInfo(name)
		if err != nil {
			toreturn := err.Error()
			r.JSON(500, map[string]interface{}{"error": toreturn})
			return
		}
		addExtensions(name, spec.Plan, dbinfo)
		r.JSON(200, map[string]string{"DATABASE_URL": "postgres://" + dbinfo.Username + ":" + dbinfo.Password + "@" + dbinfo.Endpoint})
		return
	}
	if !available {
		r.JSON(503, map[string]string{"DATABASE_URL": ""})
		return
	}
}

func delete(params martini.Params, r render.Render) {
	name := params["name"]
	region := os.Getenv("REGION")

	var plan string
	dberr := pool.QueryRow("SELECT plan from provision where name='" + name + "'").Scan(&plan)
	if dberr != nil {
		fmt.Println(dberr)
		toreturn := dberr.Error()
		r.JSON(500, map[string]interface{}{"error": toreturn})
		return
	}
	fmt.Println(plan)

	if plan == "micro" {
		_, err := pool_hobby.Exec("DROP DATABASE " + name)
		if err != nil {
			fmt.Println(err)
			toreturn := err.Error()
			r.JSON(500, map[string]interface{}{"error": toreturn})
			return
		}

	} else {
		svc := rds.New(session.New(&aws.Config{
			Region: aws.String(region),
		}))
		dparams := &rds.DeleteDBInstanceInput{
			DBInstanceIdentifier: aws.String(name), // Required
			SkipFinalSnapshot:    aws.Bool(true),
		}
		_, derr := svc.DeleteDBInstance(dparams)
		if derr != nil {
			fmt.Println(derr.Error())
			errorout := make(map[string]interface{})
			errorout["error"] = derr.Error()
			r.JSON(500, errorout)
			return
		}
	}

	fmt.Println("# Deleting")
	stmt, err := pool.Prepare("delete from provision where name=$1")
	if err != nil {
		errorout := make(map[string]interface{})
		errorout["error"] = err.Error()
		r.JSON(500, errorout)
		return
	}
	res, err := stmt.Exec(name)
	if err != nil {
		errorout := make(map[string]interface{})
		errorout["error"] = err.Error()
		r.JSON(500, errorout)
		return
	}
	affect, err := res.RowsAffected()
	if err != nil {
		errorout := make(map[string]interface{})
		errorout["error"] = err.Error()
		r.JSON(500, errorout)
		return
	}
	fmt.Println(affect, "rows changed")

	r.JSON(200, map[string]interface{}{"status": "deleted"})
}

func plans(r render.Render) {
	plans := make(map[string]interface{})
	plans["micro"] = "Shared Tenancy"
	plans["small"] = "2x CPU - 4GB Mem - 20GB Disk - Extra IOPS:no"
	plans["medium"] = "2x CPU - 8GB Mem - 50GB Disk - Extra IOPS:no"
	plans["large"] = " 4x CPU - 30GB Mem - 100GB Disk - Extra IOPS:1000"
	r.JSON(200, plans)
}

func url(params martini.Params, r render.Render) {
	name := params["name"]
	dbinfo, err := getDBInfo(name)
	if err != nil {
		toreturn := err.Error()
		r.JSON(500, map[string]interface{}{"error": toreturn})
		return
	}
	r.JSON(200, map[string]string{"DATABASE_URL": "postgres://" + dbinfo.Username + ":" + dbinfo.Password + "@" + dbinfo.Endpoint})
}

func getDBInfo(name string) (dbinfo dbspec, err error) {
	const USERNAME = "masteruser"
	const PASSWORD = "masterpass"
	const ENDPOINT = "endpoint"
	dbinfo.Username = queryDB(USERNAME, name)
	dbinfo.Password = queryDB(PASSWORD, name)
	dbinfo.Endpoint = queryDB(ENDPOINT, name)
	return dbinfo, nil
}

func queryDB(i string, name string) string {
	dberr := pool.QueryRow("select " + i + " from provision where name ='" + name + "'").Scan(&i)
	if dberr != nil {
		return ""
	}
	fmt.Println(name)
	printDBStats(pool)
	return i
}

func isAvailable(name string) bool {
	var toreturn bool
	region := os.Getenv("REGION")

	svc := rds.New(session.New(&aws.Config{
		Region: aws.String(region),
	}))

	rparams := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(name),
		MaxRecords:           aws.Int64(20),
	}
	rresp, rerr := svc.DescribeDBInstances(rparams)
	if rerr != nil {
		fmt.Println(rerr)
	}
	//      fmt.Println(rresp)
	fmt.Println("Checking to see if available...")
	fmt.Println(*rresp.DBInstances[0].DBInstanceStatus)
	status := *rresp.DBInstances[0].DBInstanceStatus
	if status == "available" {
		toreturn = true
	}
	if status != "available" {
		toreturn = false
	}
	return toreturn
}

func addExtensions(name string, plan string, dbinfo dbspec) (e error) {
	masteruser := dbinfo.Username
	masterpass := dbinfo.Password
	endpoint := dbinfo.Endpoint

	uri := "postgres://" + masteruser + ":" + masterpass + "@" + endpoint

	if plan == "micro" {
		uri = gethobbyurl() + "/" + name
	}

	db, dberr := sql.Open("postgres", uri)
	if dberr != nil {
		fmt.Println(dberr)
		return dberr
	}

	_, err := db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public")
	if err != nil {
		fmt.Println(err)
		db.Close()
		return err
	}
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS tablefunc WITH SCHEMA public")
	if err != nil {
		fmt.Println(err)
		db.Close()
		return err
	}
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS hstore WITH SCHEMA public")
	if err != nil {
		fmt.Println(err)
		db.Close()
		return err
	}
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\" WITH SCHEMA public")
	if err != nil {
		db.Close()
		return err
	}
	db.Close()
	return nil
}

func gethobbyurl() string {
	value := os.Getenv("HOBBYDB")
	if len(value) == 0 {
		return os.Getenv("HOBBY_DB")
	}
	return value
}

type provisionspec struct {
	Plan        string `json:"plan"`
	Billingcode string `json:"billingcode"`
}
type tagspec struct {
	Resource string `json:"resource"`
	Name     string `json:"name"`
	Value    string `json:"value"`
}
type dbspec struct {
	Username string
	Password string
	Endpoint string
}

func printDBStats(db *sql.DB) {
	fmt.Printf("Open connections: %v\n", db.Stats().OpenConnections)
}
