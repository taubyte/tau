package project_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPrettyBasic(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	assert.DeepEqual(t, project.Prettify(nil), map[string]any{
		"Id":          "projectID1",
		"Name":        "TrueTest",
		"Description": "a simple test project",
		"Tags":        []string{"tag1", "tag2"},
		"Email":       "cto@taubyte.com",
		"Databases": map[string]any{
			"test_database1": map[string]any{
				"Encryption-Type": "",
				"Id":              "database1ID",
				"Description":     "a database for users",
				"Regex":           true,
				"Local":           false,
				"Secret":          false,
				"Min":             15,
				"Name":            "test_database1",
				"Tags":            []string{"database_tag_1", "database_tag_2"},
				"Match":           "/users",
				"Max":             30,
				"Size":            "5GB",
			},
		},
		"SmartOps": map[string]any{
			"test_smartops1": map[string]any{
				"Memory":      "16MB",
				"Call":        "ping1",
				"Id":          "smartops1ID",
				"Name":        "test_smartops1",
				"Description": "verifies node has GPU",
				"Tags":        []string{"smart_tag_1", "smart_tag_2"},
				"Source":      ".",
				"Timeout":     "6m40s",
			},
		},
		"Websites": map[string]any{
			"test_website1": map[string]any{
				"Branch":      "main",
				"GitId":       "111111111",
				"GitFullName": "taubyte-test/photo_booth",
				"Id":          "website1ID",
				"Name":        "test_website1",
				"Description": "a simple photo booth",
				"Tags":        []string{"website_tag_1", "website_tag_2"},
				"Domains":     []string{"test_domain1"},
				"Paths":       []string{"/photos"},
				"GitProvider": "github",
			},
		},
		"Domains": map[string]any{
			"test_domain1": map[string]any{
				"Id":             "domain1ID",
				"Name":           "test_domain1",
				"Description":    "a domain for hal computers",
				"Tags":           []string{"domain_tag_1", "domain_tag_2"},
				"FQDN":           "hal.computers.com",
				"UseCertificate": true,
				"Type":           "inline",
			},
		},
		"Services": map[string]any{
			"test_service1": map[string]any{
				"Id":          "service1ID",
				"Name":        "test_service1",
				"Description": "a super simple protocol",
				"Tags":        []string{"service_tag_1", "service_tag_2"},
				"Protocol":    "/simple/v1",
			},
		},
		"Messaging": map[string]any{
			"test_messaging1": map[string]any{
				"Description":  "a messaging channel",
				"Local":        false,
				"MQTT":         false,
				"WebSocket":    true,
				"Id":           "messaging1ID",
				"Name":         "test_messaging1",
				"ChannelMatch": "simple1",
				"Tags":         []string{"messaging_tag_1", "messaging_tag_2"},
				"Regex":        false,
			},
		},
		"Storages": map[string]any{
			"test_storage1": map[string]any{
				"Name":        "test_storage1",
				"Tags":        []string{"storage_tag_1", "storage_tag_2"},
				"Match":       "photos",
				"Regex":       true,
				"TTL":         "5m",
				"Id":          "storage1ID",
				"Description": "a streaming storage",
				"Size":        "30GB",
				"Type":        "streaming",
			},
		},
		"Applications": map[string]any{
			"test_app2": map[string]any{
				"Id":          "application2ID",
				"Name":        "test_app2",
				"Description": "this is test app 2",
				"Tags":        []string{"app_tag_3", "app_tag_4"},
				"Functions": map[string]any{
					"test_function3": map[string]any{
						"Type":        "p2p",
						"Timeout":     "1h15m",
						"Memory":      "64GB",
						"Command":     "command3",
						"Local":       false,
						"Id":          "function3ID",
						"Name":        "test_function3",
						"Source":      ".",
						"Call":        "ping3",
						"Protocol":    "",
						"Description": "a p2p function for ping over peer-2-peer",
						"Tags":        []string{"function_tag_5", "function_tag_6"},
					},
				},
			},
			"test_app1": map[string]any{
				"Id":          "application1ID",
				"Name":        "test_app1",
				"Description": "this is test app 1",
				"Tags":        []string{"app_tag_1", "app_tag_2"},
				"Services": map[string]any{
					"test_service2": map[string]any{
						"Id":          "service2ID",
						"Name":        "test_service2",
						"Description": "a simple protocol",
						"Tags":        []string{"service_tag_3", "service_tag_4"},
						"Protocol":    "/simple/v2",
					},
				},
				"Libraries": map[string]any{
					"test_library2": map[string]any{
						"Id":          "library2ID",
						"Name":        "test_library2",
						"Description": "just another library",
						"GitProvider": "github",
						"GitFullName": "taubyte-test/library2",
						"Tags":        []string{"library_tag_3", "library_tag_4"},
						"Path":        "/src",
						"Branch":      "dream",
						"GitId":       "222222222",
					},
				},
				"Messaging": map[string]any{
					"test_messaging2": map[string]any{
						"Id":           "messaging2ID",
						"Name":         "test_messaging2",
						"Description":  "another messaging channel",
						"ChannelMatch": "simple2",
						"Tags":         []string{"messaging_tag_3", "messaging_tag_4"},
						"Local":        true,
						"Regex":        true,
						"MQTT":         true,
						"WebSocket":    false,
					},
				},
				"Databases": map[string]any{
					"test_database2": map[string]any{
						"Local":           true,
						"Encryption-Type": "",
						"Tags":            []string{"database_tag_3", "database_tag_4"},
						"Name":            "test_database2",
						"Description":     "a profiles database",
						"Match":           "profiles",
						"Regex":           false,
						"Secret":          false,
						"Min":             42,
						"Max":             601,
						"Id":              "database2ID",
						"Size":            "45GB",
					},
				},
				"Functions": map[string]any{
					"test_function2": map[string]any{
						"Tags":        []string{"function_tag_3", "function_tag_4"},
						"Type":        "pubsub",
						"Source":      "library/test_library2",
						"Memory":      "23MB",
						"Call":        "ping2",
						"Channel":     "channel2",
						"Id":          "function2ID",
						"Name":        "test_function2",
						"Local":       true,
						"Description": "a pubsub function on channel 2 with a call to a library",
						"Timeout":     "23s",
					},
				},
				"Websites": map[string]any{
					"test_website2": map[string]any{
						"Description": "my portfolio",
						"Branch":      "main",
						"GitProvider": "github",
						"GitId":       "222222222",
						"Id":          "website2ID",
						"Name":        "test_website2",
						"Tags":        []string{"website_tag_3", "website_tag_4"},
						"Domains":     []string{"test_domain2"},
						"Paths":       []string{"/portfolio"},
						"GitFullName": "taubyte-test/portfolio",
					},
				},
				"Storages": map[string]any{
					"test_storage2": map[string]any{
						"Description": "an object storage",
						"Tags":        []string{"storage_tag_3", "storage_tag_4"},
						"Type":        "object",
						"Regex":       false,
						"Size":        "50GB",
						"Public":      true,
						"Versioning":  true,
						"Id":          "storage2ID",
						"Name":        "test_storage2",
						"Match":       "users",
					},
				},
				"Domains": map[string]any{
					"test_domain2": map[string]any{
						"Tags":           []string{"domain_tag_3", "domain_tag_4"},
						"FQDN":           "app.computers.com",
						"UseCertificate": false,
						"Type":           "",
						"Id":             "domain2ID",
						"Name":           "test_domain2",
						"Description":    "a domain for app computers",
					},
				},
				"SmartOps": map[string]any{
					"test_smartops2": map[string]any{
						"Description": "verifies user is on a specific continent",
						"Tags":        []string{"smart_tag_3", "smart_tag_4"},
						"Source":      "library/test_library2",
						"Timeout":     "5m",
						"Memory":      "64MB",
						"Call":        "ping2",
						"Id":          "smartops2ID",
						"Name":        "test_smartops2",
					},
				},
			},
		},
		"Libraries": map[string]any{
			"test_library1": map[string]any{
				"Branch":      "main",
				"GitProvider": "github",
				"GitId":       "111111111",
				"Description": "just a library",
				"Tags":        []string{"library_tag_1", "library_tag_2"},
				"Path":        "/",
				"Id":          "library1ID",
				"Name":        "test_library1",
				"GitFullName": "taubyte-test/library1",
			},
		},
		"Functions": map[string]any{
			"test_function1": map[string]any{
				"Tags":        []string{"function_tag_1", "function_tag_2"},
				"Memory":      "32GB",
				"Call":        "ping1",
				"Paths":       []string{"/ping1"},
				"Name":        "test_function1",
				"Description": "an http function for a simple ping",
				"Type":        "http",
				"Source":      ".",
				"Timeout":     "20s",
				"Method":      "get",
				"Domains":     []string{"test_domain1"},
				"Id":          "function1ID",
			},
		},
	})
}
