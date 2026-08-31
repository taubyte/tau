package internal

import (
	_ "embed"
)

// Database
var (
	//go:embed config/databases/test_database1.yaml
	Database1 []byte

	//go:embed config/applications/test_app1/databases/test_database2.yaml
	Database2 []byte
)

// Domain
var (
	//go:embed config/domains/test_domain1.yaml
	Domain1 []byte

	//go:embed config/applications/test_app1/domains/test_domain2.yaml
	Domain2 []byte
)

// Function
var (
	//go:embed config/functions/test_function1.yaml
	Function1 []byte

	//go:embed config/applications/test_app1/functions/test_function2.yaml
	Function2 []byte

	//go:embed config/applications/test_app2/functions/test_function3.yaml
	Function3 []byte
)

// Library
var (
	//go:embed config/libraries/test_library1.yaml
	Library1 []byte

	//go:embed config/applications/test_app1/libraries/test_library2.yaml
	Library2 []byte
)

// Messaging
var (
	//go:embed config/messaging/test_messaging1.yaml
	Messaging1 []byte

	//go:embed config/applications/test_app1/messaging/test_messaging2.yaml
	Messaging2 []byte
)

// Service
var (
	//go:embed config/services/test_service1.yaml
	Service1 []byte

	//go:embed config/applications/test_app1/services/test_service2.yaml
	Service2 []byte
)

// SmartOp
var (
	//go:embed config/smartops/test_smartops1.yaml
	SmartOp1 []byte

	//go:embed config/applications/test_app1/smartops/test_smartops2.yaml
	SmartOp2 []byte
)

// Storage
var (
	//go:embed config/storages/test_storage1.yaml
	Storage1 []byte

	//go:embed config/applications/test_app1/storages/test_storage2.yaml
	Storage2 []byte
)

// Website
var (
	//go:embed config/websites/test_website1.yaml
	Website1 []byte

	//go:embed config/applications/test_app1/websites/test_website2.yaml
	Website2 []byte
)
