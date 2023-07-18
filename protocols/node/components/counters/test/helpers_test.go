package test

// TODO: Counters as plugin
// import (
// 	"fmt"
// 	"os"
// 	"path"
// 	"strconv"
// 	"strings"
// 	"time"

// 	"bitbucket.org/taubyte/config-compiler/decompile"
// 	dreamlandCommon "github.com/taubyte/dreamland/core/common"
// 	dreamland "github.com/taubyte/dreamland/core/services"
// 	commonIface "github.com/taubyte/go-interfaces/common"
// 	counterSpec "github.com/taubyte/go-interfaces/services/substrate/counters"
// 	structureSpec "github.com/taubyte/go-specs/structure"
// 	"github.com/taubyte/odo/protocols/monkey/fixtures/compile"
// )

// var successfulServiceableCounterCount = 6

// func init() {
// 	counterSpec.DefaultReportTime = 5 * time.Second
// }

// func (scm *servCounterMetrics) display() {
// 	averageTotalSuccess := time.Duration(scm.successTime) / time.Duration(scm.successCount)
// 	averageTotalColdStart := time.Duration(scm.successColdStartSuccessTime) / time.Duration(scm.successCount)
// 	averageTotalExecution := time.Duration(scm.successExecutionTime) / time.Duration(scm.successCount)
// 	fmt.Printf(`
// Serviceable %s
// 	Failed: %d Times
// 	Succeeded: %d Times

// Successfull Call Averages:
// 	Average Total Time: %s
// 	Average ColdStart: %s
// 	Average Execution: %s

// `, scm.basePath, scm.failCount, scm.successCount, averageTotalSuccess, averageTotalColdStart, averageTotalExecution)

// }

// func handleCountVal(toAdd *int, resp []byte) error {
// 	val, err := strconv.Atoi(string(resp))
// 	if err != nil {
// 		return err
// 	}

// 	*toAdd += val
// 	return nil
// }

// func handleTimeVal(toAdd *float64, resp []byte) error {
// 	val, err := strconv.ParseFloat(string(resp), 64)
// 	if err != nil {
// 		return err
// 	}

// 	*toAdd += val
// 	return nil
// }

// func handleKey(resp []byte, key string, servsToTest ...*servCounterMetrics) (err error) {
// 	var matchedServ *servCounterMetrics
// 	var strippedKey string
// 	for _, servToTest := range servsToTest {
// 		if strings.Contains(key, servToTest.basePath) {
// 			matchedServ = servToTest
// 			strippedKey = path.Join(servToTest.basePath, strings.Split(key, servToTest.basePath)[1])
// 			break
// 		}
// 	}

// 	basePath := counterSpec.NewPath(matchedServ.basePath)
// 	sc, st := basePath.SuccessMetricPaths()
// 	scsc, scst := basePath.SuccessColdStartMetricPaths()
// 	sec, set := basePath.SuccessExecutionMetricPaths()
// 	fc, ft := basePath.FailMetricPaths()
// 	fcssc, fcsst, fcsfc, fcsft := basePath.FailColdStartMetricPaths()
// 	fec, fet := basePath.FailExecutionMetricPaths()

// 	switch strippedKey {
// 	case sc:
// 		err = handleCountVal(&matchedServ.successCount, resp)
// 	case st:
// 		err = handleTimeVal(&matchedServ.successTime, resp)
// 	case scsc:
// 		err = handleCountVal(&matchedServ.successColdStartSuccessCount, resp)
// 	case scst:
// 		err = handleTimeVal(&matchedServ.successColdStartSuccessTime, resp)
// 	case sec:
// 		err = handleCountVal(&matchedServ.successExecutionCount, resp)
// 	case set:
// 		err = handleTimeVal(&matchedServ.successExecutionTime, resp)
// 	case fc:
// 		err = handleCountVal(&matchedServ.failCount, resp)
// 	case ft:
// 		err = handleTimeVal(&matchedServ.failTime, resp)
// 	case fcssc:
// 		err = handleCountVal(&matchedServ.failColdStartFailCount, resp)
// 	case fcsst:
// 		err = handleTimeVal(&matchedServ.failColdStartSuccessTime, resp)
// 	case fcsfc:
// 		err = handleCountVal(&matchedServ.failColdStartFailCount, resp)
// 	case fcsft:
// 		err = handleTimeVal(&matchedServ.failColdStartFailTime, resp)
// 	case fec:
// 		err = handleCountVal(&matchedServ.failExecutionCount, resp)
// 	case fet:
// 		err = handleTimeVal(&matchedServ.failExecutionTime, resp)
// 	}

// 	return
// }

// func getDefs(structures []interface{}) (defs []structureDef, err error) {
// 	domains := make(map[string]string, 0)
// 	var functions []*structureSpec.Function
// 	var websites []*structureSpec.Website

// 	for _, structure := range structures {
// 		switch _structure := structure.(type) {
// 		case *structureSpec.Website:
// 			websites = append(websites, _structure)
// 		case *structureSpec.Function:
// 			functions = append(functions, _structure)
// 		case *structureSpec.Domain:
// 			domains[_structure.Name] = _structure.Fqdn
// 		default:
// 			return nil, fmt.Errorf("Unable to process type `%T` must be a domain, function, or website", structure)
// 		}
// 	}

// 	if len(domains) == 0 || len(functions)+len(websites) == 0 {
// 		return nil, fmt.Errorf("Invalid structures, at least 1 domain, and function or website is required")
// 	}

// 	for _, function := range functions {
// 		fqdn, err := getFqdnFromDomains(domains, function.Domains[0])
// 		if err != nil {
// 			return nil, err
// 		}

// 		defs = append(defs, structureDef{id: function.Id, fqdn: fqdn, path: function.Paths[0]})
// 	}

// 	for _, website := range websites {
// 		fqdn, err := getFqdnFromDomains(domains, website.Domains[0])
// 		if err != nil {
// 			return nil, err
// 		}

// 		defs = append(defs, structureDef{id: website.Id, fqdn: fqdn, path: website.Paths[0]})
// 	}

// 	return
// }

// func getFqdnFromDomains(domains map[string]string, name string) (string, error) {
// 	fqdn, ok := domains[name]
// 	if ok == false {
// 		return "", fmt.Errorf("Unable to use domain `%s`, domain structure has not been defined", name)
// 	}

// 	return fqdn, nil
// }

// func getUrls(u dreamlandCommon.Universe, structures []structureDef) (urls []string, err error) {
// 	nodePort, err := u.GetPortHttp(u.Node().Node())
// 	if err != nil {
// 		return nil, fmt.Errorf("Getting node port failed with: %s", err)
// 	}

// 	for _, req := range structures {
// 		urls = append(urls, fmt.Sprintf("http://%s:%d%s", req.fqdn, nodePort, req.path))
// 	}

// 	return
// }

// func startUniverse(structures []interface{}) (dreamlandCommon.Universe, error) {
// 	u := dreamland.Multiverse("TestCounters")

// 	handleError := func(err error) (dreamlandCommon.Universe, error) {
// 		if err != nil {
// 			u.Stop()
// 			return nil, err
// 		}

// 		return u, err
// 	}

// 	err := u.StartWithConfig(&dreamlandCommon.Config{
// 		Services: map[string]commonIface.ServiceConfig{
// 			"tns":     {},
// 			"node":    {},
// 			"hoarder": {},
// 			"billing": {},
// 		},
// 		Simples: map[string]dreamlandCommon.SimpleConfig{
// 			"client": {
// 				Clients: dreamlandCommon.SimpleConfigClients{
// 					TNS: &commonIface.ClientConfig{},
// 				},
// 			},
// 		},
// 	})
// 	if err != nil {
// 		return handleError(fmt.Errorf("Starting universe with config failed with: %s", err))
// 	}

// 	time.Sleep(5 * time.Second)

// 	project, err := decompile.MockBuild(testProjectId, "", structures...)

// 	if err != nil {
// 		return handleError(fmt.Errorf("Mock build failed for project `%s` failed with: %s", testProjectId, err))
// 	}

// 	err = u.RunFixture("injectProject", project)
// 	if err != nil {
// 		return handleError(fmt.Errorf("Running fixture injectProject failed with: %s", err))
// 	}

// 	wd, err := os.Getwd()
// 	if err != nil {
// 		return handleError(fmt.Errorf("Getting working directory failed with: %s", err))
// 	}

// 	err = u.RunFixture("compileFor", compile.BasicCompileFor{
// 		ProjectId:  testProjectId,
// 		ResourceId: testFuncId1,
// 		// Uncomment uncomment to rebuild go file to tmp
// 		// Path: path.Join(wd, "_assets", "counters1.go"),
// 		Paths: []string{path.Join(wd, "_assets", "counters1.zwasm")},
// 	})
// 	if err != nil {
// 		return handleError(fmt.Errorf("Running fixture compileFor for func1 failed with: %s", err))
// 	}

// 	return handleError(u.RunFixture("compileFor", compile.BasicCompileFor{
// 		ProjectId:  testProjectId,
// 		ResourceId: testFuncId2,
// 		// Uncomment uncomment to rebuild go file to tmp
// 		// Path: path.Join(wd, "_assets", "counters2.go"),
// 		Paths: []string{path.Join(wd, "_assets", "counters2.zwasm")},
// 	}))
// }
