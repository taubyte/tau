package service

import "time"

var (
	NodeDatabaseFileName     string = "node-database.db"
	IPKey                           = "IP"
	DefaultBlockTime                = 60 * time.Second
	ValidServiceResponseTime        = 5 * time.Minute
)

const (
	UsageStatement = `INSERT OR REPLACE INTO Usage(Id, Name, Timestamp, UsedMemory, TotalMemory, FreeMemory, TotalCpu, CpuCount, User, Nice, System, Idle, Iowait, Irq, Softirq, Steal, Guest, GuestNice, StatCount, Address, TotalDisk, FreeDisk, UsedDisk, AvailableDisk) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	InsertService  = `INSERT OR REPLACE INTO Services(Id, Timestamp, Type) VALUES (?,?,?)`
	InsertMeta     = `INSERT OR REPLACE INTO Meta(Id, Type, Key, Value) VALUES (?,?,?,?)`
	DeleteService  = `DELETE FROM Services WHERE Id=?`
	DeleteMetas    = `DELETE FROM Meta WHERE Id=?`
	GetServiceType = `SELECT Type FROM Services WHERE Id=?`

	CreateServiceTable = `CREATE TABLE IF NOT EXISTS Services (
		"Id" varchar(255),
		"Timestamp" int,
		"Type"  varchar(255),
		UNIQUE(Id, Type)
	);`

	CreateUsageTable = `CREATE TABLE IF NOT EXISTS Usage (
		"Id" varchar(255) UNIQUE,
		"Name" varchar(255),
		"Timestamp" int,
		"UsedMemory" int,
		"TotalMemory" int,
		"FreeMemory" int,
		"TotalCpu" int,
		"CpuCount" int,
		"User" int,
		"Nice" int,
		"System" int,
		"Idle" int,
		"Iowait" int,
		"Irq" int,
		"Softirq" int,
		"Steal" int,
		"Guest" int,
		"GuestNice" int,
		"StatCount" int,
		"Address" varchar(255),
		"TotalDisk" int,
		"FreeDisk" int,
		"UsedDisk" int,
		"AvailableDisk" int
	  );`

	CreateMetaTable = `CREATE TABLE IF NOT EXISTS Meta (
		"Id" varchar(255),
		"Type"  varchar(255),
		"Key" varchar(255),
		"Value" varchar(255),
		UNIQUE(Id, Key, Type)
	  );`

	// TODO: Combine these two Statements
	GetStableNodeIps = `SELECT Value from Meta, Usage WHERE Meta.Id = Usage.Id AND Usage.Timestamp > ? AND Key="IP" AND Type="node";`
	GetServiceIp     = `SELECT Value from Meta, Usage WHERE Meta.Id = Usage.Id AND Usage.Timestamp > ? AND Key="IP" AND  Type=?;`
)
