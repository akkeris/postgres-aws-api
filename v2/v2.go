package v2

import (
	"database/sql"
	"os"
	"math/rand"
	"log"
	"fmt"
	"time"
	"strings"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/go-martini/martini"
	_ "github.com/lib/pq"
	"github.com/martini-contrib/binding"
	"github.com/martini-contrib/render"
)

// TODO: Ensure we can rotate in a new hobby database
//       without issues.

const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var pool *sql.DB
var pool_hobby *sql.DB
var awssvc *rds.RDS


type DatabaseUrlSpec struct {
	Username string
	Password string
	Endpoint string
	Plan string
}

type DatabaseSpec struct {
	Name string `json:"name"`
}

type FullDatabaseSpec struct {
	Name string `json:"name"`
	Plan string `json:"plan"`
	Claimed string `json:"claimed"`
	Created string `json:"created_at"`
	Host string `json:"endpoint"`
	Username string `json:"username"`
	Password string `json:"password"`
	DATABASE_URL string `json:"DATABASE_URL"`
}

type DatabaseBackupSpec struct {
	Database DatabaseSpec `json:"database"`
	Id *string `json:"id"`
	Progress *int64 `json:"progress"`
	Status *string `json:"status"`
	Created string `json:"created_at"`
}

type DatabaseLogs struct {
	Size *int64 `json:"size"`
	Name *string `json:"name"`
	Updated string `json:"updated_at"`
}

type Provision struct {
	Plan        string `json:"plan"`
	Billingcode string `json:"billingcode"`
}

type Tag struct {
	Resource string `json:"resource"`
	Name string `json:"name"`
	Value string `json:"value"`
}

func Start(m *martini.ClassicMartini, provision_db *sql.DB, hobby_db *sql.DB) {
	pool = provision_db;
	pool_hobby = hobby_db;
    rand.Seed(time.Now().UnixNano())
	
	awssvc = rds.New(session.New(&aws.Config{
		Region: aws.String(os.Getenv("REGION")),
	}))

	m.Get("/v2/postgres", listDbs)
	m.Get("/v2/postgres/plans", plans)
	m.Get("/v2/postgres/:name/backups", listBackups)
	m.Get("/v2/postgres/:name/backups/:backup", getBackup)
	m.Put("/v2/postgres/:name/backups", createBackup)
	m.Put("/v2/postgres/:name/backups/:backup", restore)
	m.Get("/v2/postgres/:name/logs", listLogs)
	m.Get("/v2/postgres/:name/logs/:dir/:file", getLogs)
	m.Put("/v2/postgres/:name", restart)
	m.Post("/v2/postgres/:name/roles", createRole)
	m.Delete("/v2/postgres/:name/roles/:role", deleteRole)
	m.Get("/v2/postgres/:name/roles", listRoles)
	m.Put("/v2/postgres/:name/roles/:role", rotateRole)
	m.Get("/v2/postgres/:name/roles/:role", getRole)
	m.Get("/v2/postgres/:name", getDb)
	m.Post("/v2/postgres", binding.Json(Provision{}), createDB)
	m.Delete("/v2/postgres/:name", deleteDB)
	m.Post("/v2/postgres/:name/tags", binding.Json(Tag{}), tagDB)
}


func randStringBytes(n int) string {
    b := make([]byte, n)
    for i := range b {
        b[i] = letters[rand.Intn(len(letters))]
    }
    return string(b)
}

func dblogs(name string) (dbinfo []*rds.DescribeDBLogFilesDetails, err error) {
	var fileLastWritten int64 = time.Now().AddDate(0,0,-7).Unix()
	var maxRecords int64 = 100

	//FilenameContains
	logs, err := awssvc.DescribeDBLogFiles(&rds.DescribeDBLogFilesInput{
	    DBInstanceIdentifier:&name,
	    FileLastWritten:&fileLastWritten,
	    MaxRecords:&maxRecords,
	})
	if err != nil {
		return nil, err
	}
	return logs.DescribeDBLogFiles, err
}

func dbArn(name string) string {
	return "arn:aws:rds:" + os.Getenv("REGION") + ":" + os.Getenv("ACCOUNTNUMBER") + ":db:" + name
}

func dbInfo(name string) (dbinfo DatabaseUrlSpec, err error) {
	err = pool.QueryRow("select plan, masteruser, masterpass, endpoint from provision where name = $1", name).Scan(&dbinfo.Plan, &dbinfo.Username, &dbinfo.Password, &dbinfo.Endpoint)
	return dbinfo, err
}

func dbs() (dbinfo []FullDatabaseSpec, err error) {
	rows, err := pool.Query("select name, plan, claimed, make_date::varchar(200) as make_date, endpoint, masteruser, masterpass from provision")
	defer rows.Close()
	if err == nil {
		for rows.Next() {
			var spec FullDatabaseSpec
			err = rows.Scan(&spec.Name, &spec.Plan, &spec.Claimed, &spec.Created, &spec.Host, &spec.Username, &spec.Password)
			spec.DATABASE_URL = "postgres://" + spec.Username + ":" + spec.Password + "@" + spec.Host
			if err != nil {
				return dbinfo, err
			}
			dbinfo = append(dbinfo, spec)
		}
	}
	return dbinfo, err
}

func getDb(params martini.Params, r render.Render) {
	name := params["name"]
	var spec FullDatabaseSpec
	err := pool.QueryRow("select name, plan, claimed, make_date::varchar(200) as make_date, endpoint, masteruser, masterpass from provision where name = $1 and claimed = 'yes'", name).Scan(&spec.Name, &spec.Plan, &spec.Claimed, &spec.Created, &spec.Host, &spec.Username, &spec.Password)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	spec.DATABASE_URL = "postgres://" + spec.Username + ":" + spec.Password + "@" + spec.Host
	r.JSON(200, spec)
}

func plans(params martini.Params, r render.Render) {
	plans := make(map[string]interface{})
	plans["micro"] = "Shared Tenancy"
	plans["small"] = "2x CPU - 4GB Mem - 20GB Disk - Extra IOPS:no"
	plans["medium"] = "2x CPU - 8GB Mem - 50GB Disk - Extra IOPS:no"
	plans["large"] = " 4x CPU - 30GB Mem - 100GB Disk - Extra IOPS:1000"
	r.JSON(200, plans)
}

func listBackups(params martini.Params, r render.Render) {
	name := params["name"]
	_, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	snapshots, err := awssvc.DescribeDBSnapshots(&rds.DescribeDBSnapshotsInput{ DBInstanceIdentifier:&name })
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	out := make([]DatabaseBackupSpec, 0)
	for _, snapshot := range snapshots.DBSnapshots {
		created := time.Now().UTC().Format(time.RFC3339)
		if snapshot.SnapshotCreateTime != nil {
			created = snapshot.SnapshotCreateTime.UTC().Format(time.RFC3339)
		}
		out = append(out, DatabaseBackupSpec{
			Database: DatabaseSpec{
				Name:name,
			},
			Id:snapshot.DBSnapshotIdentifier,
			Progress:snapshot.PercentProgress,
			Status:snapshot.Status,
			Created:created,
		})
	}
	r.JSON(200, out)
}

func getBackup(params martini.Params, r render.Render) {
	name := params["name"]
	info, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if info.Plan == "micro" {
		r.JSON(422, map[string]interface{}{"error": "Obtaining backups are not available for this plan"})
		return
	}
	id := params["backup"]
	snapshots, err := awssvc.DescribeDBSnapshots(&rds.DescribeDBSnapshotsInput{ 
		DBInstanceIdentifier:&name,
		DBSnapshotIdentifier:&id,
	})
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if len(snapshots.DBSnapshots) != 1 {
		r.JSON(404, map[string]interface{}{"error": "Snapshot not found."})
		return
	}

	created := time.Now().UTC().Format(time.RFC3339)
	if snapshots.DBSnapshots[0].SnapshotCreateTime != nil {
		created = snapshots.DBSnapshots[0].SnapshotCreateTime.UTC().Format(time.RFC3339)
	}

	r.JSON(200, DatabaseBackupSpec{
		Database: DatabaseSpec{
			Name:name,
		},
		Id:snapshots.DBSnapshots[0].DBSnapshotIdentifier,
		Progress:snapshots.DBSnapshots[0].PercentProgress,
		Status:snapshots.DBSnapshots[0].Status,
		Created:created,
	})
}

func createBackup(params martini.Params, r render.Render) {
	name := params["name"]
	info, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if info.Plan == "micro" {
		r.JSON(422, map[string]interface{}{"error": "Creating backups are not available for this plan"})
		return
	}
	snapshot_name := (name + "-manual-" + randStringBytes(10))
	snapshot, err := awssvc.CreateDBSnapshot(&rds.CreateDBSnapshotInput{ 
		DBInstanceIdentifier:&name,
		DBSnapshotIdentifier:&snapshot_name,
	})
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	
	created := time.Now().UTC().Format(time.RFC3339)
	if snapshot.DBSnapshot.SnapshotCreateTime != nil {
		created = snapshot.DBSnapshot.SnapshotCreateTime.UTC().Format(time.RFC3339)
	}

	r.JSON(200, DatabaseBackupSpec{
		Database: DatabaseSpec{
			Name:name,
		},
		Id:snapshot.DBSnapshot.DBSnapshotIdentifier,
		Progress:snapshot.DBSnapshot.PercentProgress,
		Status:snapshot.DBSnapshot.Status,
		Created:created,
	})
}

func listDbs(params martini.Params, r render.Render) {
	alldbs, err := dbs()
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	r.JSON(200, alldbs)
}

func restore(params martini.Params, r render.Render) {
	name := params["name"]
	info, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if info.Plan == "micro" {
		r.JSON(422, map[string]interface{}{"error": "Databases cannot be automatically restored for this plan."})
		return
	}
	backup := params["backup"]
	_, err = awssvc.RestoreDBInstanceFromDBSnapshot(&rds.RestoreDBInstanceFromDBSnapshotInput{ 
		DBInstanceIdentifier:&name,
		DBSnapshotIdentifier:&backup,
	})
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}

	r.JSON(200, map[string]interface{}{"status": "OK"})
}

func listLogs(params martini.Params, r render.Render) {
	name := params["name"]
	info, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if info.Plan == "micro" {
		r.JSON(422, map[string]interface{}{"error": "Logs are not available for this plan"})
		return
	}
	logs, err := dblogs(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}

	out := make([]DatabaseLogs, 0)
	for _, log := range logs {
		updated := time.Now().UTC().Format(time.RFC3339)
		if log.LastWritten != nil {
			updated = time.Unix(*log.LastWritten/1000, 0).UTC().Format(time.RFC3339)
		}
		out = append(out, DatabaseLogs{
			Name:log.LogFileName,
			Size:log.Size,
			Updated:updated,
		})
	}
	r.JSON(200, out)
}

func getLogs(params martini.Params, r render.Render) {
	name := params["name"]
	info, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if info.Plan == "micro" {
		r.JSON(422, map[string]interface{}{"error": "Logs are not available for this plan"})
		return
	}

	dir := params["dir"]
	file := params["file"]
	path := (dir + "/" + file)
	data, err := awssvc.DownloadDBLogFilePortion(&rds.DownloadDBLogFilePortionInput{
	    DBInstanceIdentifier:&name,
	    LogFileName:&path,
	})
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if data.LogFileData == nil {
		r.Text(200, "")
	} else {
		r.Text(200, *data.LogFileData)
	}
}

func restart(params martini.Params, r render.Render) {
	name := params["name"]
	info, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	if info.Plan == "micro" {
		r.JSON(422, map[string]interface{}{"error": "Micro databases cannot be automatically restored."})
		return
	}
	_, err = awssvc.RebootDBInstance(&rds.RebootDBInstanceInput{
		DBInstanceIdentifier:&name,
	})
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	r.JSON(200, map[string]interface{}{"status": "OK"})
}

func createRole(params martini.Params, r render.Render) {
	statement := `
	do $$
	begin
	  create user $1 with login encrypted password $2;
	  grant select on all tables in schema public TO $1;
	  grant usage, select on all sequences in schema public TO $1;
	  grant connect on database $3 to $1;
	  alter default privileges in schema public GRANT SELECT ON TABLES TO $1;
	  REVOKE CREATE ON SCHEMA public FROM $1;
	  GRANT USAGE ON SCHEMA public TO $1;

	  ALTER DEFAULT PRIVILEGES FOR USER $4 IN SCHEMA public GRANT SELECT ON SEQUENCES TO $1;
	  ALTER DEFAULT PRIVILEGES FOR USER $4 IN SCHEMA public GRANT SELECT ON TABLES TO $1;
	end 
	$$;
	`
	name := params["name"]
	dbinfo, err := dbInfo(name)
	if err != nil {
		log.Printf("Unable to find backing database: %s\n", err.Error())
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	
	app_username := dbinfo.Username
	master_username := dbinfo.Username
	master_password := dbinfo.Password

	if dbinfo.Plan == "micro" {
		// retrieve the master username/pass for hobby database.
		host := strings.Split(dbinfo.Endpoint, "/")
		err := pool.QueryRow("select masteruser, masterpass from shared_tenant where host = $1",  host[0]).Scan(&master_username, &master_password)
		if err != nil {
			log.Printf("Unable to find shared tenant credentials: %s %s\n", name, err.Error())
			r.JSON(500, map[string]interface{}{"error": err.Error()})
			return
		}
	}
	db, err := sql.Open("postgres", "postgres://" + master_username + ":" + master_password + "@" + dbinfo.Endpoint)
	if err != nil {
		log.Printf("Unable to connect to backing database: %s\n", err.Error())
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	defer db.Close()
	username := "rdo1" + strings.ToLower(randStringBytes(7))
	password := randStringBytes(10)
	_, err = db.Exec(strings.Replace(strings.Replace(strings.Replace(strings.Replace(statement, "$1", username , -1), "$2", "'" + password + "'", -1), "$3",  name , -1), "$4", app_username, -1))
	
	if err != nil {
		log.Printf("Unable to create user on backing database: %s\n", err.Error())
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	_, err = pool.Exec("insert into extra_roles (database, username, passwd, read_only, make_date, update_date) values ($1, $2, $3, $4, now(), now())", name, username, password, true)
	if err != nil {
		// TODO: Drop user?
		log.Printf("Unable to insert the role, there may be an orphen user [%s]: %s\n", username, err.Error())
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	r.JSON(201, DatabaseUrlSpec{
		Username:username,
		Password:password,
		Endpoint:dbinfo.Endpoint,
	})
}

func deleteRole(params martini.Params, r render.Render) {
	name := params["name"]
	role := params["role"]
	statement := `
	do $$
	begin
	  ALTER DEFAULT PRIVILEGES FOR USER $3 IN SCHEMA public REVOKE SELECT ON SEQUENCES FROM $1;
	  ALTER DEFAULT PRIVILEGES FOR USER $3 IN SCHEMA public REVOKE SELECT ON TABLES FROM $1;

	  revoke usage on schema public FROM $1;
	  revoke connect on database $2 from $1;
	  revoke select on all tables in schema public from $1;
	  revoke usage, select on all sequences in schema public from $1;
	  alter default privileges in schema public REVOKE SELECT ON TABLES FROM $1;
	  drop user $1;
	end 
	$$;
	`
	dbinfo, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}

	app_username := dbinfo.Username
	master_username := dbinfo.Username
	master_password := dbinfo.Password

	if dbinfo.Plan == "micro" {
		// retrieve the master username/pass for hobby database.
		host := strings.Split(dbinfo.Endpoint, "/")
		err := pool.QueryRow("select masteruser, masterpass from shared_tenant where host = $1",  host[0]).Scan(&master_username, &master_password)
		if err != nil {
			log.Printf("Unable to find shared tenant credentials: %s %s\n", name, err.Error())
			r.JSON(500, map[string]interface{}{"error": err.Error()})
			return
		}
	}

	db, err := sql.Open("postgres", "postgres://" + master_username + ":" + master_password + "@" + dbinfo.Endpoint)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	defer db.Close()
	_, err = db.Exec(strings.Replace(strings.Replace(strings.Replace(statement, "$1", role, -1), "$2", name, -1), "$3", app_username, -1))
	
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	_, err = pool.Exec("delete from extra_roles where database = $1 and username = $2", name, role)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	r.JSON(200, map[string]interface{}{"status": "OK"})
}

func listRoles(params martini.Params, r render.Render) {
	name := params["name"]
	dbinfo, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	rows, err := pool.Query("SELECT username, passwd FROM extra_roles where database = $1", name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	defer rows.Close()
	var roles []DatabaseUrlSpec
	for rows.Next() {
		var role DatabaseUrlSpec
		role.Endpoint = dbinfo.Endpoint
		if err := rows.Scan(&role.Username, &role.Password); err != nil {
			r.JSON(500, map[string]interface{}{"error": err.Error()})
			return
		}
		roles = append(roles, role)
	}
	r.JSON(200, roles)
}

func getRole(params martini.Params, r render.Render) {
	name := params["name"]
	role := params["role"]
	dbinfo, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	rows, err := pool.Query("SELECT username, passwd FROM extra_roles where database = $1 and username = $2", name, role)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	defer rows.Close()
	var roles []DatabaseUrlSpec
	for rows.Next() {
		var role DatabaseUrlSpec
		role.Endpoint = dbinfo.Endpoint
		if err := rows.Scan(&role.Username, &role.Password); err != nil {
			r.JSON(500, map[string]interface{}{"error": err.Error()})
			return
		}
		roles = append(roles, role)
	}

	if len(roles) != 1 {
		r.JSON(404,  map[string]interface{}{"error":"The specified role was not found."})
		return
	}
	r.JSON(200, roles[0])
}

func rotateRole(params martini.Params, r render.Render) {
	name := params["name"]
	role := params["role"]

	dbinfo, err := dbInfo(name)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}

	master_username := dbinfo.Username
	master_password := dbinfo.Password

	if dbinfo.Plan == "micro" {
		// retrieve the master username/pass for hobby database.
		host := strings.Split(dbinfo.Endpoint, "/")
		err := pool.QueryRow("select masteruser, masterpass from shared_tenant where host = $1",  host[0]).Scan(&master_username, &master_password)
		if err != nil {
			log.Printf("Unable to find shared tenant credentials: %s %s\n", name, err.Error())
			r.JSON(500, map[string]interface{}{"error": err.Error()})
			return
		}
	}

	db, err := sql.Open("postgres", "postgres://" + master_username + ":" + master_password + "@" + dbinfo.Endpoint)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	defer db.Close()
	password := randStringBytes(10)
	_, err = db.Exec("alter user " + role + " WITH PASSWORD '" + password + "'")
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	_, err = pool.Exec("update extra_roles set passwd=$3 where database = $1 and username = $2", name, role, password)
	if err != nil {
		r.JSON(500, map[string]interface{}{"error": err.Error()})
		return
	}
	r.JSON(200, DatabaseUrlSpec{
		Username:role,
		Password:password,
		Endpoint:dbinfo.Endpoint,
	})
}

func createDB(spec Provision, berr binding.Errors, r render.Render) {
	plan := spec.Plan
	billingcode := spec.Billingcode

	var name string
	dberr := pool.QueryRow("select name from provision where plan='" + plan + "' and claimed='no' and make_date=(select min(make_date) from provision where plan='" + plan + "' and claimed='no')").Scan(&name)
	if dberr != nil {
		fmt.Printf("ERROR: No database with the plan [%s] was found unclaimed. Check the preprovisioner for more information.\n", name, dberr.Error())
		r.JSON(500, map[string]interface{}{"error": dberr.Error()})
		return
	}
	fmt.Printf("v2/v2.go:createDB: Found %s and beginning to provision it\n", name)

	instanceName := name
	if spec.Plan == "micro" {
		instanceName = os.Getenv("HOBBY_INSTANCE_NAME")
	}

	available := isAvailable(instanceName)
	if available {
		var dbinfo DatabaseUrlSpec
		_, dberr := pool.Exec("update provision set claimed=$1 where name=$2", "yes", name)

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

			_, awserr := svc.AddTagsToResource(params)
			if awserr != nil {
				fmt.Println(awserr.Error())
				toreturn := awserr.Error()
				r.JSON(500, map[string]interface{}{"error": toreturn})
				return
			}
		}

		dbinfo, err := dbInfo(name)
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
		fmt.Println("Unable to find any available pg databases for " + spec.Plan)
		r.JSON(503, map[string]string{"DATABASE_URL": ""})
		return
	}
}

func deleteDB(params martini.Params, r render.Render) {
	name := params["name"]
	region := os.Getenv("REGION")

	var plan string
	var endpoint string
	dberr := pool.QueryRow("SELECT plan, endpoint from provision where name='" + name + "'").Scan(&plan, &endpoint)
	if dberr != nil {
		fmt.Printf("Failed to find provisioned database [%s]: %s\n", name, dberr.Error())
		r.JSON(500, map[string]interface{}{"error": dberr.Error()})
		return
	}

	if plan == "micro" {
		var masteruser string
		var masterpass string 
		var host string
		host = strings.Split(endpoint, "/")[0]
		err := pool.QueryRow("select masterpass, masteruser, host from shared_tenant where host = $1", host).Scan(&masterpass, &masteruser, &host)
		if err != nil {
			fmt.Printf("Failed to find shared tenant master credentials [%s]: %s\n", name, dberr.Error())
			r.JSON(500, map[string]interface{}{"error": dberr.Error()})
			return
		}
		db, err := sql.Open("postgres", "postgres://" + masteruser + ":" + masterpass + "@" + host)
		if err != nil {
			fmt.Printf("Failed to connect to shared tenant system [%s]: %s\n", name, dberr.Error())
			r.JSON(500, map[string]interface{}{"error": dberr.Error()})
			return
		}
		defer db.Close()
		_, err = db.Exec("DROP DATABASE " + name)
		if err != nil {
			fmt.Printf("Failed to drop database [%s]: %s", name, err.Error())
			r.JSON(500, map[string]interface{}{"error": err.Error()})
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

	_, err := pool.Exec("delete from provision where name=$1", name)
	if err != nil {
		errorout := make(map[string]interface{})
		errorout["error"] = err.Error()
		r.JSON(500, errorout)
		return
	}
	r.JSON(200, map[string]interface{}{"status": "deleted"})
}

func tagDB(spec Tag, berr binding.Errors, r render.Render) {
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

// move below to utils

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

func addExtensions(name string, plan string, dbinfo DatabaseUrlSpec) (e error) {
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
	defer db.Close()

	_, err := db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public")
	if err != nil {
		fmt.Println(err)
		return err
	}
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS tablefunc WITH SCHEMA public")
	if err != nil {
		fmt.Println(err)
		return err
	}
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS hstore WITH SCHEMA public")
	if err != nil {
		fmt.Println(err)
		return err
	}
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\" WITH SCHEMA public")
	if err != nil {
		return err
	}
	return nil
}

func gethobbyurl() string {
	value := os.Getenv("HOBBYDB")
	if len(value) == 0 {
		return os.Getenv("HOBBY_DB")
	}
	return value
}

