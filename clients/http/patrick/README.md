# go-patrick-http

REST client for Patrick Node

# Testing


## Run

```
go test ./...
```


# Usage

```go
// jid is a job id

c := New(ctx, URL(url string), Auth(token string), Provider(provider string))
jids, _ := c.Jobs(projectId string)

for _, jid := range jobs {
    _job, _ := c.Job(jid)
    _job.Logs // cid of the job's log file
    _job.Status // The status of the job  ( commonPatrick.JobStatus )
    _job.Meta.Repository.Id // The repository the job was ran on

    _job.TimeStamp // NOT IMPLEMENTED, TODO made in patrick/service/http_job.go

    readCloser, _ := c.LogFile(_job.Logs)

    // Print logs of the job to stdOut
    os.Copy(os.Stdout, readCloser)
}

```





