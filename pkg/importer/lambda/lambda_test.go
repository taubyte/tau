package lambda

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
)

func importOf(t *testing.T, s string) importer.Result {
	t.Helper()
	r, err := Import([]byte(s))
	require.NoError(t, err)
	return r
}

// docOf is one imported function's document.
func docOf(t *testing.T, r importer.Result, name string) map[string]any {
	t.Helper()
	doc, ok := r.Doc("function", name)
	require.True(t, ok, "no function called %q; got %v", name, r.Names("function"))
	return doc
}

func finding(t *testing.T, r importer.Result, resource, key string) importer.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Resource == resource && f.Key == key {
			return f
		}
	}
	t.Fatalf("no finding for %q/%q", resource, key)
	return importer.Finding{}
}

func count(r importer.Result, key string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Key == key {
			n++
		}
	}
	return n
}

// TestTheShapeMismatchIsSaidOnce, up front. Every function in the file has it,
// and repeating it per function would bury the findings that differ.
func TestTheShapeMismatchIsSaidOnce(t *testing.T) {
	r := importOf(t, `
functions:
  hello:
    handler: src/hello.handler
  goodbye:
    handler: src/goodbye.handler
`)
	require.Equal(t, 1, count(r, "handler"))

	f := finding(t, r, "", "handler")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "HANDLER_NOT_FETCH_SHAPED", f.Code)
	require.Contains(t, f.Detail, "(event, context)")
}

// TestBothDialectsAreRead, because a caller has one file and should not have to
// know which parser to reach for.
func TestBothDialectsAreRead(t *testing.T) {
	sam := importOf(t, `
Transform: AWS::Serverless-2016-10-31
Resources:
  Checkout:
    Type: AWS::Serverless::Function
    Properties:
      Handler: app.handler
      MemorySize: 512
      Timeout: 30
`)
	require.Equal(t, map[string]any{"memory": "512MB", "timeout": "30s"},
		docOf(t, sam, "Checkout")["execution"])

	sls := importOf(t, `
service: shop
provider:
  runtime: nodejs20.x
  memorySize: 256
functions:
  checkout:
    handler: src/checkout.handler
    timeout: 15
`)
	require.Equal(t, map[string]any{"memory": "256MB", "timeout": "15s"},
		docOf(t, sls, "checkout")["execution"])
}

// TestGlobalsAndProviderSupplyDefaults, or a function that set nothing would
// import with no limits at all.
func TestGlobalsAndProviderSupplyDefaults(t *testing.T) {
	r := importOf(t, `
Globals:
  Function:
    MemorySize: 1024
    Timeout: 60
Resources:
  Worker:
    Type: AWS::Serverless::Function
    Properties:
      Handler: worker.handler
`)
	require.Equal(t, map[string]any{"memory": "1024MB", "timeout": "60s"},
		docOf(t, r, "Worker")["execution"])
}

// TestACloudFormationIntrinsicIsNotAValue. !Ref and Fn::GetAtt resolve against a
// stack that will not exist here, so carrying one would put a dangling
// reference in the config.
func TestACloudFormationIntrinsicIsNotAValue(t *testing.T) {
	r := importOf(t, `
Resources:
  Api:
    Type: AWS::Serverless::Function
    Properties:
      Handler: api.handler
      Environment:
        Variables:
          TABLE_NAME:
            Ref: OrdersTable
          QUEUE_URL:
            Fn::GetAtt: [Queue, Arn]
          PLAIN: literal
`)
	require.Equal(t, map[string]any{"PLAIN": map[string]any{"value": "literal"}},
		docOf(t, r, "Api")["variables"])

	require.Equal(t, importer.Blocked, finding(t, r, "Api", "environment.TABLE_NAME").Level)
	require.Equal(t, "CLOUDFORMATION_INTRINSIC", finding(t, r, "Api", "environment.QUEUE_URL").Code)
}

// TestAContainerImageRunsAndSaysWhatDiffers.
//
// It embeds AWS's runtime client and polls for work rather than listening —
// which used to make it the one import this platform could not finish. The
// container tier now serves the Lambda Runtime API, so the image runs
// unmodified; what is left is the shape of the event it receives, and that is
// a real difference rather than a caveat.
func TestAContainerImageIsTheHarderOneNotTheEasier(t *testing.T) {
	r := importOf(t, `
Resources:
  Img:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
      ImageUri: 1234.dkr.ecr.us-east-1.amazonaws.com/app:1
`)
	f := finding(t, r, "Img", "image")
	require.Equal(t, importer.Approximated, f.Level, "it runs")
	require.Equal(t, "IMAGE_INVOKED_OVER_HTTP", f.Code)
	require.Contains(t, f.Detail, "polls the Lambda Runtime API")
	require.Contains(t, f.Detail, "runs unmodified")
	require.Contains(t, f.Detail, "API Gateway payload format 2.0")
}

// TestSnapStartIsFlaggedLoudly. Nothing else in the manifest reveals that a
// function's cold-start profile changes by an order of magnitude.
func TestSnapStartIsFlaggedLoudly(t *testing.T) {
	r := importOf(t, `
Resources:
  Api:
    Type: AWS::Serverless::Function
    Properties:
      Handler: api.handler
      SnapStart:
        ApplyOn: PublishedVersions
`)
	f := finding(t, r, "Api", "snapStart")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "order of magnitude")
}

// TestEachTriggerSaysWhatItWouldNeed — this is where "not Fetch-shaped" becomes
// concrete, because a Lambda's event shape is per trigger.
func TestEachTriggerSaysWhatItWouldNeed(t *testing.T) {
	r := importOf(t, `
Resources:
  Api:
    Type: AWS::Serverless::Function
    Properties:
      Handler: api.handler
      Events:
        Web:
          Type: HttpApi
        Nightly:
          Type: Schedule
        Uploads:
          Type: S3
`)
	http := finding(t, r, "Api", "events.HttpApi")
	require.Equal(t, importer.Approximated, http.Level)
	require.Contains(t, http.Detail, "the code changes even though the routing carries")

	require.Equal(t, "NO_TAU_FEATURE:cron", finding(t, r, "Api", "events.Schedule").Code)

	s3 := finding(t, r, "Api", "events.S3")
	require.Equal(t, importer.Blocked, s3.Level)
	require.Contains(t, s3.Detail, "AWS-shaped event")
}

func TestLayersAndVpcAreNamed(t *testing.T) {
	r := importOf(t, `
functions:
  api:
    handler: api.handler
    layers:
      - arn:aws:lambda:us-east-1:1234:layer:deps:3
    vpc:
      securityGroupIds: [sg-1]
      subnetIds: [subnet-1]
`)
	l := finding(t, r, "api", "layers")
	require.Equal(t, importer.Blocked, l.Level)
	require.NotEmpty(t, l.Needs)

	require.Equal(t, importer.Blocked, finding(t, r, "api", "vpc").Level)
}

// TestPluginsAreNotData. A serverless plugin rewrites the deployment at package
// time, and what it produces cannot be read from the file.
func TestPluginsAreNotData(t *testing.T) {
	r := importOf(t, `
service: shop
plugins:
  - serverless-webpack
  - serverless-offline
functions:
  api:
    handler: api.handler
`)
	f := finding(t, r, "", "plugins")
	require.Equal(t, "SOURCE_NOT_DATA", f.Code)
	require.Contains(t, f.Detail, "serverless-webpack")
	require.Empty(t, f.Needs, "arbitrary code is a refusal, not a gap")
}

// TestIamIsElsewhere. What a function may reach is defined in the account, not
// in the manifest — and that is an account connection, not something to build.
func TestIamIsElsewhere(t *testing.T) {
	r := importOf(t, "functions:\n  api:\n    handler: api.handler\n")
	f := finding(t, r, "", "iam")
	require.Equal(t, importer.Elsewhere, f.Level)
	require.Empty(t, f.Needs)
}

// TestNonFunctionResourcesAreSkipped. A SAM template is CloudFormation and
// carries tables, queues and buckets that are not functions.
func TestNonFunctionResourcesAreSkipped(t *testing.T) {
	r := importOf(t, `
Resources:
  Orders:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: orders
  Api:
    Type: AWS::Serverless::Function
    Properties:
      Handler: api.handler
      MemorySize: 128
`)
	require.Equal(t, []string{"Api"}, r.Names("function"))
}

func TestAFileWithNoFunctionsIsRefused(t *testing.T) {
	_, err := Import([]byte("Resources:\n  Orders:\n    Type: AWS::DynamoDB::Table\n"))
	require.Error(t, err)

	_, err = Import([]byte("{not yaml"))
	require.Error(t, err)
}

// TestNothingBlockedReachesTheEmittedConfig — the rule every importer holds.
func TestNothingBlockedReachesTheEmittedConfig(t *testing.T) {
	r := importOf(t, `
functions:
  api:
    handler: api.handler
    memorySize: 512
    layers: [arn:layer:1]
    vpc: {subnetIds: [subnet-1]}
    snapStart: true
`)
	doc := docOf(t, r, "api")
	for _, key := range []string{"layers", "vpc", "snapStart", "handler", "memory", "timeout", "env"} {
		require.NotContains(t, doc, key, "%s reached the config", key)
	}
	require.Equal(t, "512MB", doc["execution"].(map[string]any)["memory"])
}

// TestTheThinEdges — a function declared with nothing under it, the same
// trigger twice, and a plain YAML map that only looks like an intrinsic.
func TestTheThinEdges(t *testing.T) {
	r := importOf(t, `
Globals:
  Function:
    Timeout: 10
Resources:
  Bare:
    Type: AWS::Lambda::Function
  Twice:
    Type: AWS::Serverless::Function
    Properties:
      Handler: api.handler
      Environment:
        Variables:
          SHAPE:
            region: us-east-1
      Events:
        A:
          Type: HttpApi
        B:
          Type: HttpApi
`)
	// Properties absent entirely: Globals still apply, and nothing panics.
	require.Equal(t, "10s", docOf(t, r, "Bare")["execution"].(map[string]any)["timeout"])

	// One finding per trigger TYPE, not per trigger. Two HTTP routes are one
	// fact about the handler.
	require.Equal(t, 1, count(r, "events.HttpApi"))

	// A map is not an intrinsic just for being a map — but it is not a string
	// either, so it is refused for the right reason.
	require.NotContains(t, docOf(t, r, "Twice"), "variables")
	require.Equal(t, "ENV_NOT_A_SCALAR", finding(t, r, "Twice", "environment.SHAPE").Code)
}

// TestScalarEnvValuesCarry — a YAML number or bool is a legitimate environment
// value and must not be refused alongside the structures.
func TestScalarEnvValuesCarry(t *testing.T) {
	r := importOf(t, `
functions:
  api:
    handler: api.handler
    environment:
      PORT: 8080
      DEBUG: true
      RATIO: 0.5
      EMPTY:
`)
	require.Equal(t, map[string]any{
		"PORT":  map[string]any{"value": "8080"},
		"DEBUG": map[string]any{"value": "true"},
		"RATIO": map[string]any{"value": "0.5"},
		"EMPTY": map[string]any{"value": ""},
	}, docOf(t, r, "api")["variables"])
}

// TestDefaultsInheritDownBothDialects — env set once at the top applies to every
// function, and a Serverless event list reads the same as a SAM event map.
func TestDefaultsInheritDownBothDialects(t *testing.T) {
	sam := importOf(t, `
Globals:
  Function:
    Environment:
      Variables:
        STAGE: prod
Resources:
  Api:
    Type: AWS::Serverless::Function
    Properties:
      Handler: api.handler
`)
	require.Equal(t, map[string]any{"STAGE": map[string]any{"value": "prod"}},
		docOf(t, sam, "Api")["variables"])

	sls := importOf(t, `
provider:
  environment:
    STAGE: prod
functions:
  api:
    handler: api.handler
    events:
      - http:
          path: /orders
          method: get
      - sqs:
          arn: arn:aws:sqs:us-east-1:1234:jobs
`)
	require.Equal(t, map[string]any{"STAGE": map[string]any{"value": "prod"}},
		docOf(t, sls, "api")["variables"])
	require.Equal(t, importer.Approximated, finding(t, sls, "api", "events.http").Level)
	require.Equal(t, "NO_TAU_FEATURE:event-source", finding(t, sls, "api", "events.sqs").Code)
}
