# run
```
openssl req -nodes -newkey rsa:2048 -keyout privkey.key -out fullchain.csr -subj "/C=US/ST=Texas/L=Dallas/O=Taubyte/OU=/CN=auth.taubyte.com"
```