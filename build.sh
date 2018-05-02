#!/bin/sh
export GOPATH=/go/src
cd /go/src
go get -u "github.com/lib/pq"
go get -u "github.com/aws/aws-sdk-go/aws"
go get -u "github.com/aws/aws-sdk-go/aws/session"
go get -u "github.com/aws/aws-sdk-go/service/rds"
go get -u "github.com/go-martini/martini"
go get -u "github.com/martini-contrib/binding"
go get -u "github.com/martini-contrib/render"
cd /go/src/oct-postgres-api
go build server.go

