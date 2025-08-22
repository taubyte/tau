# OpenURI

## Usage


Open the file.
```go
o, err := uri.Open("/path/to/file")
```

Open the URL.
```go
o, err := uri.Open("http://localhost")
```

with Google App Engine
```go
o, err := uri.Open("http://localhost", uri.WithHTTPClient(urlfetch.Client(ctx)))
```

## Example

```go
	//
	// Open a local file
	//
	o, err := uri.Open("/path/to/file")
	if err != nil {
	    log.Fatal(err)
	}
	defer o.Close()
	
	b, _ := ioutil.ReadAll(o)
	log.Println(string(b))
	
	//
	// Open URL
	//
	o, err = uri.Open("http://localhost")
	if err != nil {
	    log.Fatal(err)
	}
	defer o.Close()
	
	b, _ = ioutil.ReadAll(o)
	log.Println(string(b))

```
