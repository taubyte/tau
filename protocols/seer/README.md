# README #

This README would normally document whatever steps are necessary to get your application up and running.

### What is this repository for? ###

* Quick summary
* Version
* [Learn Markdown](https://bitbucket.org/tutorials/markdowndemo)

### How do I get set up? ###

* Summary of set up
* Configuration
* Dependencies
* Database configuration
* How to run tests
* Deployment instructions

### Contribution guidelines ###

* Writing tests
* Code review
* Other guidelines

### Who do I talk to? ###

* Repo owner or admin
* Other community or team contact

### Working with DNS test ###
Make sure you delete db file whenver you run the test as IP changes everytime.

To access the db file make sure you have sqlite3 installed.
Go into /service/test then run 
```shell
sqlite3 file-name
```

Some other calls below that may help

To run a call to a port in command line
```shell
dig @127.0.0.1 -p port# 
```

To run a call using tcp in command line
```shell
dig @127.0.0.1 -p port# +vc
```

To run a call with a specific domain in command line
```shell
dig @127.0.0.1 -p port# +vc a poop.com a "dns"
```

# Deploy to Prod
Go into /service/service.go and under New(), uncomment the p2pPeer.Datastore line before pushing

```
cd spore/prod
go run .
```