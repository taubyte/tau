package compiler

// This object was hand converted from the object that is created by the project compiler
// it is made for comparison in testing.
// the only conversions were:
// []interface{}{} => []interface{}{}
// rawint => uint64(rawint)

var createdProjectObject = map[interface{}]interface{}{
	"applications": map[interface{}]interface{}{
		"someappID": map[interface{}]interface{}{
			"databases": map[interface{}]interface{}{
				"testid": map[interface{}]interface{}{
					"description": "Test Database",
					"local":       false,
					"match":       "testMatchlocal",
					"max":         uint64(2),
					"min":         uint64(1),
					"name":        "test_database_l",
					"size":        uint64(5000000000),
					"tags": []interface{}{
						"tag1",
						"tag2",
					},
					"useRegex": true,
				},
			},
			"description": "some app description",
			"domains": map[interface{}]interface{}{
				"testAppdomain": map[interface{}]interface{}{
					"cert-type":   "inlineApp",
					"description": "Test Appdomain",
					"fqdn":        "taubyte.local.com",
					"name":        "test_domain_l",
					"tags": []interface{}{
						"tagAppdomain1",
						"tagAppdomain2",
					},
				},
			},
			"functions": map[interface{}]interface{}{
				"testlocalID": map[interface{}]interface{}{
					"call":        "entry",
					"description": "some local description",
					"domains": []interface{}{
						"testAppdomain",
						"testdomainid",
					},
					"source": "libraries/QmXmed7RDZN77LV5uuwwuutuUHvzEMyUdtGTHFMM8LOCAL",
					"memory": uint64(10000000000),
					"method": "post",
					"name":   "test_function_l",
					"paths": []interface{}{
						"/example",
					},
					"secure": false,
					"smartops": []interface{}{
						"testAppsmartOps",
					},
					"tags": []interface{}{
						"some",
						"tag",
						"smartops:test_smartops_l",
					},
					"timeout": uint64(10000000000),
					"type":    "http",
				},
			},
			"libraries": map[interface{}]interface{}{
				"QmXmed7RDZN77LV5uuwwuutuUHvzEMyUdtGTHFMM8LOCAL": map[interface{}]interface{}{
					"branch":          "main",
					"description":     "test library description",
					"name":            "test_library_l",
					"path":            "/",
					"provider":        "github",
					"repository-id":   "460685870",
					"repository-name": "tb_library_testLibrary",
					"tags": []interface{}{
						"local",
						"free",
					},
				},
			},
			"messaging": map[interface{}]interface{}{
				"testid": map[interface{}]interface{}{
					"description": "Test Messaging",
					"local":       true,
					"match":       "some match",
					"mqtt":        false,
					"name":        "test_messaging_l",
					"regex":       false,
					"tags": []interface{}{
						"tag1",
						"tag2",
					},
					"webSocket": false,
				},
			},
			"name": "someApp",
			"services": map[interface{}]interface{}{
				"testAppservice": map[interface{}]interface{}{
					"description": "Test Appservice",
					"name":        "test_service_l",
					"tags": []interface{}{
						"tagAppservice1",
						"tagAppservice2",
					},
					"protocol": "/testprotocol/v1",
				},
			},
			"smartops": map[interface{}]interface{}{
				"testAppsmartOps": map[interface{}]interface{}{
					"call":        "entrypoint6",
					"description": "Test AppsmartOps",
					"source":      "libraries/QmXmed7RDZN77LV5uuwwuutuUHvzEMyUdtGTHFMM8LOCAL",
					"memory":      uint64(64000000),
					"name":        "test_smartops_l",
					"timeout":     uint64(300000000000),
				},
			},
			"storages": map[interface{}]interface{}{
				"QmV2KtAPhZHjFhH4iWXZFkWzB92iFUVHWScLNU5YELOCAL": map[interface{}]interface{}{
					"description": "this is a storage",
					"match":       "testMatchStorageLocal",
					"name":        "test_storage_l",
					"public":      true,
					"size":        uint64(10000000),
					"tags": []interface{}{
						"private",
						"free",
					},
					"ttl":      uint64(20000000000),
					"type":     "streaming",
					"useRegex": true,
				},
			},
			"tags": []interface{}{
				"tag1",
				"tag2",
			},
			"websites": map[interface{}]interface{}{
				"QmZNzaehW4USdQ5tYQQNFao5D7Szp5S9x3TiKfLOCAL": map[interface{}]interface{}{
					"branch":      "main",
					"description": "test website description",
					"domains": []interface{}{
						"testAppdomain",
						"testdomainid",
					},
					"name": "test_website_l",
					"paths": []interface{}{
						"/apple",
					},
					"provider":        "github",
					"repository-id":   "460911436",
					"repository-name": "tb_website_testWebsite",
					"tags": []interface{}{
						"local",
						"free",
					},
				},
			},
		},
	},
	"databases": map[interface{}]interface{}{
		"testidglobal": map[interface{}]interface{}{
			"description": "Test Database das globe",
			"local":       false,
			"match":       "testMatchglobal",
			"max":         uint64(2),
			"min":         uint64(1),
			"name":        "test_database_g",
			"size":        uint64(5000000000),
			"tags": []interface{}{
				"tag1",
				"tag2",
				"glob",
			},
			"useRegex": true,
		},
	},
	"description": "Test Project",
	"domains": map[interface{}]interface{}{
		"testdomainid": map[interface{}]interface{}{
			"cert-file":   "testCert",
			"cert-type":   "inline",
			"description": "test_domain",
			"fqdn":        "taubyte.global.com",
			"key-file":    "testKey",
			"name":        "test_domain_g",
			"tags": []interface{}{
				"tagdomain1",
				"tagdomain2",
			},
		},
	},
	"functions": map[interface{}]interface{}{
		"QmdZsK4VyNdUUs1EQZS33iR6UzFKwffDpwiFdNGVupmFe6": map[interface{}]interface{}{
			"call":        "testEntry",
			"channel":     "testChannel",
			"description": "pubsub description",
			"source":      ".",
			"domains": []interface{}{
				"testdomainid",
			},
			"local":  true,
			"memory": uint64(10000000),
			"name":   "test_function_gpubsub",
			"tags": []interface{}{
				"pubsub",
				"test",
			},
			"timeout": uint64(600000000000),
			"type":    "pubsub",
		},
		"testhttp": map[interface{}]interface{}{
			"call":        "entry",
			"description": "some description",
			"source":      "libraries/QmXmed7RDZN77LV5EbUpSEtuUHvzEMyUdtGTHFMMGLOBAL",
			"memory":      uint64(10000000000),
			"method":      "post",
			"name":        "test_function_ghttp",
			"paths": []interface{}{
				"/example",
			},
			"secure": false,
			"smartops": []interface{}{
				"testsmartOpsid",
			},
			"tags": []interface{}{
				"some",
				"tag",
				"smartops:test_smartops_g",
			},
			"timeout": uint64(10000000000),
			"type":    "http",
		},
		"testp2p": map[interface{}]interface{}{
			"call":        "entry",
			"command":     "testCommand",
			"description": "p2p description",
			"source":      "libraries/QmXmed7RDZN77LV5EbUpSEtuUHvzEMyUdtGTHFMMGLOBAL",
			"local":       false,
			"memory":      uint64(10000),
			"name":        "test_function_gp2p",
			"tags": []interface{}{
				"p2p",
				"test",
			},
			"timeout": uint64(10000000000),
			"type":    "p2p",
			"service": "/test/v1",
		},
	},
	"id":    "testid",
	"email": "test@taubyte.com",
	"libraries": map[interface{}]interface{}{
		"QmXmed7RDZN77LV5EbUpSEtuUHvzEMyUdtGTHFMMGLOBAL": map[interface{}]interface{}{
			"branch":          "main",
			"description":     "test library description",
			"name":            "test_library_g",
			"path":            "/",
			"provider":        "github",
			"repository-id":   "460685870",
			"repository-name": "tb_library_testLibrary",
			"tags": []interface{}{
				"local",
				"free",
			},
		},
	},
	"messaging": map[interface{}]interface{}{
		"testidglobal": map[interface{}]interface{}{
			"description": "Test Messaging das globe",
			"local":       true,
			"match":       "some match",
			"mqtt":        true,
			"name":        "test_messaging_g",
			"regex":       false,
			"tags": []interface{}{
				"tag1",
				"tag2",
				"glob",
			},
			"webSocket": false,
		},
	},
	"name": "test_project",
	"services": map[interface{}]interface{}{
		"testserviceid": map[interface{}]interface{}{
			"description": "test_service",
			"name":        "test_service_g",
			"tags": []interface{}{
				"tagservice1",
				"tagservice2",
			},
			"protocol": "/testprotocol/v2",
		},
	},
	"smartops": map[interface{}]interface{}{
		"testsmartOpsid": map[interface{}]interface{}{
			"call":        "entryp",
			"description": "Test smartOpstion",
			"source":      ".",
			"memory":      uint64(64000000),
			"name":        "test_smartops_g",
			"tags": []interface{}{
				"tagsmartOps1",
				"tagsmartOps2",
			},
			"timeout": uint64(300000000000),
		},
	},
	"storages": map[interface{}]interface{}{
		"QmV2KtAPhZHjFhH4iWXZFkWzB92iFUVHWScLNU5YEGLOBAL": map[interface{}]interface{}{
			"description": "this is a storage",
			"match":       "testMatchStorageStreamingGlobal",
			"name":        "test_storage_streaming_g",
			"public":      true,
			"size":        uint64(10000000),
			"tags": []interface{}{
				"private",
				"free",
			},
			"ttl":      uint64(20000000000),
			"type":     "streaming",
			"useRegex": true,
		},
		"QmVaeAmXrE4Zy94BYp3CG5UKDhmvB4gTdk72pG1oyKVbAe": map[interface{}]interface{}{
			"description": "this is a storage",
			"match":       "testMatchStorageObjectGlobal",
			"name":        "test_storage_object_g",
			"public":      false,
			"size":        uint64(10000000),
			"tags": []interface{}{
				"private",
				"free",
			},
			"type":       "object",
			"useRegex":   true,
			"versioning": false,
		},
	},
	"websites": map[interface{}]interface{}{
		"QmZNzaehW4USdQ5tYQQNFao5D7Szp5S9x3TiKfGLOBAL": map[interface{}]interface{}{
			"branch":      "main",
			"description": "test website description",
			"domains": []interface{}{
				"testdomainid",
			},
			"name": "test_website_g",
			"paths": []interface{}{
				"/banana",
			},
			"provider":        "github",
			"repository-id":   "460911436",
			"repository-name": "tb_website_testWebsite",
			"tags": []interface{}{
				"local",
				"free",
			},
		},
	},
}
