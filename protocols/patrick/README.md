# README #

Meet Patrick a node that listens to git hooks and create jobs

### Run locally
```shell
go run -tags dev  .
```
### To run Reannounce Test
Go in service/service.go and change ReAnnounceFailedJobsTime to seconds instead of minutes

# Deploy to Prod
```
cd spore/prod
go run .
```