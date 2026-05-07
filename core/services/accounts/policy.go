package accounts

// VerifyOnAuth gates the auth/monkey ↔ accounts integration. Default true
// (production); dream/init.go flips it to false. Read at service construction.
var VerifyOnAuth = true
