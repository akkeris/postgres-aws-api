FROM golang:1.9.5
RUN mkdir -p /go/src/oct-postgres-api
WORKDIR /go/src/oct-postgres-api
COPY . .
WORKDIR /
ADD build.sh /build.sh
RUN chmod +x /build.sh
RUN /build.sh
WORKDIR /go/src/oct-postgres-api
#RUN mkdir /root/.aws
#ADD credentials /root/.aws/credentials
CMD ["/go/src/oct-postgres-api/server"]
EXPOSE 3000

