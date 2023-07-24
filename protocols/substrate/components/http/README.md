### Running Example

Add 
127.0.1.2   testing_website_builder.com 
to /etc/hosts

### Issues with Build/Test
Initializing VM is broken on the example due to other clients being nil.
A quick fix is to replace vm and in plugins/taubyte/instance.go comment ipfs,pubsub,storage in 
(p *plugin) New()

If you are having issues with build or test, 
you might be having problems with connecting to a node 
that has not been closed from a previous session. 

Run: 
netstat -ntaupe | grep 1100 | grep -o "[0-9]*/" | grep -o "[0-9]*" | xargs kill -9

Check to see if Ports are in use: 
lsof -i -P -n | grep LISTEN
