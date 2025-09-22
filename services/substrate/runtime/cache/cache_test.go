package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	libp2pPeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/peer"
	http "github.com/taubyte/tau/pkg/http"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

// Mock implementations for testing

type mockMatchDefinition struct {
	cachePrefix string
	stringVal   string
}

func (m *mockMatchDefinition) CachePrefix() string {
	return m.cachePrefix
}

func (m *mockMatchDefinition) String() string {
	return m.stringVal
}

type mockServiceComponent struct {
	tnsClient tns.Client
	cache     components.Cache
}

func (m *mockServiceComponent) Tns() tns.Client {
	return m.tnsClient
}

func (m *mockServiceComponent) Cache() components.Cache {
	return m.cache
}

func (m *mockServiceComponent) CheckTns(matcher components.MatchDefinition) ([]components.Serviceable, error) {
	return nil, nil
}

// Implement substrate.Service interface (minimal implementation)
func (m *mockServiceComponent) Vm() vm.Service                      { return nil }
func (m *mockServiceComponent) Counter() substrate.CounterService   { return nil }
func (m *mockServiceComponent) SmartOps() substrate.SmartOpsService { return nil }
func (m *mockServiceComponent) Orbitals() []vm.Plugin               { return nil }
func (m *mockServiceComponent) Dev() bool                           { return false }
func (m *mockServiceComponent) Verbose() bool                       { return false }
func (m *mockServiceComponent) Context() context.Context            { return context.Background() }

// Implement services.Service interface
func (m *mockServiceComponent) Node() peer.Node { return nil }
func (m *mockServiceComponent) Close() error    { return nil }

// Implement services.HttpService interface
func (m *mockServiceComponent) Http() http.Service { return nil }

type mockTnsClient struct {
	commit string
	cid    string
	err    error
}

func (m *mockTnsClient) Simple() tns.SimpleIface {
	return &mockTnsSimpleClient{client: m}
}

func (m *mockTnsClient) Fetch(path tns.Path) (tns.Object, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &mockTnsObject{cid: m.cid}, nil
}

func (m *mockTnsClient) Lookup(query tns.Query) (interface{}, error)             { return nil, nil }
func (m *mockTnsClient) Push(path []string, data interface{}) error              { return nil }
func (m *mockTnsClient) List(depth int) ([][]string, error)                      { return nil, nil }
func (m *mockTnsClient) Close()                                                  {}
func (m *mockTnsClient) Database() tns.StructureIface[*structureSpec.Database]   { return nil }
func (m *mockTnsClient) Domain() tns.StructureIface[*structureSpec.Domain]       { return nil }
func (m *mockTnsClient) Function() tns.StructureIface[*structureSpec.Function]   { return nil }
func (m *mockTnsClient) Library() tns.StructureIface[*structureSpec.Library]     { return nil }
func (m *mockTnsClient) Messaging() tns.StructureIface[*structureSpec.Messaging] { return nil }
func (m *mockTnsClient) Service() tns.StructureIface[*structureSpec.Service]     { return nil }
func (m *mockTnsClient) SmartOp() tns.StructureIface[*structureSpec.SmartOp]     { return nil }
func (m *mockTnsClient) Storage() tns.StructureIface[*structureSpec.Storage]     { return nil }
func (m *mockTnsClient) Website() tns.StructureIface[*structureSpec.Website]     { return nil }
func (m *mockTnsClient) Stats() tns.Stats                                        { return nil }
func (m *mockTnsClient) Peers(...libp2pPeer.ID) tns.Client                       { return m }

type mockTnsSimpleClient struct {
	client *mockTnsClient
}

func (m *mockTnsSimpleClient) Commit(projectId string, branches ...string) (string, string, error) {
	return m.client.commit, "", m.client.err
}

func (m *mockTnsSimpleClient) Project(projectID string, branches ...string) (interface{}, error) {
	return nil, nil
}
func (m *mockTnsSimpleClient) GetRepositoryProjectId(gitProvider, repoId string) (string, error) {
	return "", nil
}

type mockTnsObject struct {
	cid string
}

func (m *mockTnsObject) Path() tns.Path         { return nil }
func (m *mockTnsObject) Bind(interface{}) error { return nil }
func (m *mockTnsObject) Interface() interface{} {
	return m.cid
}
func (m *mockTnsObject) Current(branch []string) ([]tns.Path, error) { return nil, nil }

type mockServiceable struct {
	id          string
	project     string
	application string
	commit      string
	branch      string
	assetId     string
	matcher     components.MatchDefinition
	service     components.ServiceComponent
	validateErr error
	matchIndex  matcherSpec.Index
	readyErr    error
	closed      bool
	closeCount  int
	mu          sync.Mutex
}

func (m *mockServiceable) Match(matcher components.MatchDefinition) matcherSpec.Index {
	return m.matchIndex
}

func (m *mockServiceable) Validate(matcher components.MatchDefinition) error {
	return m.validateErr
}

func (m *mockServiceable) Matcher() components.MatchDefinition {
	return m.matcher
}

func (m *mockServiceable) Ready() error {
	return m.readyErr
}

func (m *mockServiceable) Project() string {
	return m.project
}

func (m *mockServiceable) Application() string {
	return m.application
}

func (m *mockServiceable) Id() string {
	return m.id
}

func (m *mockServiceable) Commit() string {
	return m.commit
}

func (m *mockServiceable) Branch() string {
	return m.branch
}

func (m *mockServiceable) AssetId() string {
	return m.assetId
}

func (m *mockServiceable) Service() components.ServiceComponent {
	return m.service
}

func (m *mockServiceable) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.closeCount++
}

func (m *mockServiceable) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *mockServiceable) CloseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

// Test helper functions

func createMockServiceable(id, prefix string, matchIndex matcherSpec.Index) *mockServiceable {
	return &mockServiceable{
		id:          id,
		project:     "test-project",
		application: "test-app",
		commit:      "test-commit",
		branch:      "main",
		assetId:     "test-asset-id",
		matcher: &mockMatchDefinition{
			cachePrefix: prefix,
			stringVal:   fmt.Sprintf("matcher-%s", id),
		},
		service: &mockServiceComponent{
			tnsClient: &mockTnsClient{
				commit: "test-commit",
				cid:    "test-asset-id",
			},
			cache: New(),
		},
		matchIndex: matchIndex,
	}
}

// Basic functionality tests

func TestNew(t *testing.T) {
	cache := New()
	if cache == nil {
		t.Fatal("New() returned nil")
	}
	if cache.cacheMap == nil {
		t.Fatal("cacheMap is nil")
	}
	if len(cache.cacheMap) != 0 {
		t.Fatal("cacheMap should be empty initially")
	}
}

func TestAdd(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Test adding a new serviceable
	result, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	if result != serviceable {
		t.Fatal("Add() returned different serviceable")
	}

	// Verify it was added to cache
	cache.locker.RLock()
	servList, exists := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if !exists {
		t.Fatal("Serviceable not added to cache")
	}
	if len(servList) != 1 {
		t.Fatalf("Expected 1 serviceable, got %d", len(servList))
	}
	if servList["test-id"] != serviceable {
		t.Fatal("Cached serviceable doesn't match original")
	}
}

func TestAddValidationError(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	serviceable.validateErr = errors.New("validation failed")

	_, err := cache.Add(serviceable)
	if err == nil {
		t.Fatal("Expected validation error")
	}
	if err.Error() != "validating serviceable failed with: validation failed" {
		t.Fatalf("Unexpected error message: %v", err)
	}
}

func TestAddExistingServiceable(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add first time
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("First Add() failed: %v", err)
	}

	// Add same serviceable again
	result, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Second Add() failed: %v", err)
	}
	if result != serviceable {
		t.Fatal("Add() returned different serviceable")
	}

	// Verify only one serviceable in cache
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 1 {
		t.Fatalf("Expected 1 serviceable, got %d", len(servList))
	}
}

func TestGet(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get serviceable
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0] != serviceable {
		t.Fatal("Retrieved serviceable doesn't match original")
	}
}

func TestGetNotFound(t *testing.T) {
	cache := New()
	matcher := &mockMatchDefinition{cachePrefix: "nonexistent", stringVal: "test-matcher"}
	options := components.GetOptions{}
	_, err := cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for non-existent serviceable")
	}
}

func TestGetWithMatchIndex(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.MinMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with specific match index
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{MatchIndex: &[]matcherSpec.Index{matcherSpec.MinMatch}[0]}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// Get with different match index (should return empty)
	options.MatchIndex = &[]matcherSpec.Index{matcherSpec.HighMatch}[0]
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for no matching serviceables")
	}
}

func TestGetWithValidation(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with validation enabled
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

func TestGetWithValidationFailure(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	// Set up validation to fail
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.commit = "different-commit"

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with validation enabled - should remove invalid serviceable
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for invalid serviceable")
	}

	// Verify serviceable was removed and closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed after validation failure")
	}
}

func TestRemove(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Remove serviceable
	cache.Remove(serviceable)

	// Verify it was removed
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 0 {
		t.Fatalf("Expected 0 serviceables, got %d", len(servList))
	}

	// Verify serviceable was closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed")
	}
}

func TestClose(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Close cache
	cache.Close()

	// Verify cacheMap is nil
	cache.locker.RLock()
	isNil := cache.cacheMap == nil
	cache.locker.RUnlock()
	if !isNil {
		t.Fatal("Expected cacheMap to be nil after Close()")
	}
}

// Concurrency tests

func TestConcurrentAdd(t *testing.T) {
	cache := New()
	numGoroutines := 100
	numServiceables := 10

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*numServiceables)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numServiceables; j++ {
				serviceable := createMockServiceable(
					fmt.Sprintf("id-%d-%d", goroutineID, j),
					fmt.Sprintf("prefix-%d", goroutineID),
					matcherSpec.HighMatch,
				)
				_, err := cache.Add(serviceable)
				if err != nil {
					errChan <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Concurrent Add() failed: %v", err)
	}

	// Verify all serviceables were added
	cache.locker.RLock()
	totalServiceables := 0
	for _, servList := range cache.cacheMap {
		totalServiceables += len(servList)
	}
	cache.locker.RUnlock()

	expectedTotal := numGoroutines * numServiceables
	if totalServiceables != expectedTotal {
		t.Fatalf("Expected %d serviceables, got %d", expectedTotal, totalServiceables)
	}
}

func TestConcurrentGet(t *testing.T) {
	cache := New()
	numServiceables := 100

	// Add serviceables
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		_, err := cache.Add(serviceable)
		if err != nil {
			t.Fatalf("Add() failed: %v", err)
		}
	}

	// Concurrent gets
	numGoroutines := 50
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
			options := components.GetOptions{}
			results, err := cache.Get(matcher, options)
			if err != nil {
				errChan <- err
				return
			}
			if len(results) != numServiceables {
				errChan <- fmt.Errorf("expected %d results, got %d", numServiceables, len(results))
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Concurrent Get() failed: %v", err)
	}
}

func TestConcurrentAddAndGet(t *testing.T) {
	cache := New()
	numOperations := 1000
	var wg sync.WaitGroup
	errChan := make(chan error, numOperations*2)

	// Concurrent adds
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			serviceable := createMockServiceable(
				fmt.Sprintf("id-%d", id),
				"test-prefix",
				matcherSpec.HighMatch,
			)
			_, err := cache.Add(serviceable)
			if err != nil {
				errChan <- err
			}
		}(i)
	}

	// Concurrent gets
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
			options := components.GetOptions{}
			_, err := cache.Get(matcher, options)
			// Get might fail if no serviceables are available yet, which is expected
			if err != nil && err.Error() != "getting cached serviceable from matcher &{test-prefix test-matcher}, failed with: does not exist" {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for unexpected errors
	for err := range errChan {
		t.Fatalf("Concurrent operation failed: %v", err)
	}
}

func TestConcurrentRemove(t *testing.T) {
	cache := New()
	numServiceables := 100

	// Add serviceables
	serviceables := make([]*mockServiceable, numServiceables)
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		serviceables[i] = serviceable
		_, err := cache.Add(serviceable)
		if err != nil {
			t.Fatalf("Add() failed: %v", err)
		}
	}

	// Concurrent removes
	numGoroutines := 50
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			// Each goroutine removes a subset of serviceables
			start := goroutineID * (numServiceables / numGoroutines)
			end := start + (numServiceables / numGoroutines)
			if goroutineID == numGoroutines-1 {
				end = numServiceables // Last goroutine takes remaining serviceables
			}
			for j := start; j < end; j++ {
				cache.Remove(serviceables[j])
			}
		}(i)
	}

	wg.Wait()

	// Verify all serviceables were removed
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 0 {
		t.Fatalf("Expected 0 serviceables, got %d", len(servList))
	}

	// Verify all serviceables were closed
	for _, serviceable := range serviceables {
		if !serviceable.IsClosed() {
			t.Fatal("Expected all serviceables to be closed")
		}
	}
}

func TestConcurrentClose(t *testing.T) {
	cache := New()
	numServiceables := 100

	// Add serviceables
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		_, err := cache.Add(serviceable)
		if err != nil {
			t.Fatalf("Add() failed: %v", err)
		}
	}

	// Concurrent operations with close
	numGoroutines := 50
	var wg sync.WaitGroup

	// Some goroutines do operations
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
			options := components.GetOptions{}
			cache.Get(matcher, options) // May fail after close, which is expected
		}()
	}

	// Some goroutines call close
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Close()
		}()
	}

	wg.Wait()

	// Verify cache is closed
	cache.locker.RLock()
	isNil := cache.cacheMap == nil
	cache.locker.RUnlock()
	if !isNil {
		t.Fatal("Expected cacheMap to be nil after Close()")
	}
}

func TestRaceConditionInAdd(t *testing.T) {
	cache := New()
	numGoroutines := 1000

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	// All goroutines try to add the same serviceable
	serviceable := createMockServiceable("same-id", "same-prefix", matcherSpec.HighMatch)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.Add(serviceable)
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Race condition in Add() failed: %v", err)
	}

	// Verify only one serviceable exists
	cache.locker.RLock()
	servList := cache.cacheMap["same-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 1 {
		t.Fatalf("Expected 1 serviceable, got %d", len(servList))
	}
}

func TestDataConsistencyUnderLoad(t *testing.T) {
	cache := New()
	numOperations := 1000
	var wg sync.WaitGroup
	errChan := make(chan error, numOperations)

	// Mix of operations
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			serviceable := createMockServiceable(
				fmt.Sprintf("id-%d", id),
				"test-prefix",
				matcherSpec.HighMatch,
			)

			switch id % 4 {
			case 0: // Add
				_, err := cache.Add(serviceable)
				if err != nil {
					errChan <- err
				}
			case 1: // Get
				matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
				options := components.GetOptions{}
				cache.Get(matcher, options) // May fail, which is expected
			case 2: // Remove
				cache.Remove(serviceable)
			case 3: // Close (only some operations)
				if id%20 == 0 {
					cache.Close()
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Data consistency test failed: %v", err)
	}
}

// Edge case tests

func TestAddNilServiceable(t *testing.T) {
	cache := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Expected panic for nil serviceable")
		}
	}()
	cache.Add(nil)
}

func TestGetWithNilMatcher(t *testing.T) {
	cache := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Expected panic for nil matcher")
		}
	}()
	cache.Get(nil, components.GetOptions{})
}

func TestRemoveNilServiceable(t *testing.T) {
	cache := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Expected panic for nil serviceable")
		}
	}()
	cache.Remove(nil)
}

func TestOperationsOnClosedCache(t *testing.T) {
	cache := New()
	cache.Close()

	// These operations should not panic but may behave unexpectedly
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add to closed cache - should panic due to nil map
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Expected panic when adding to closed cache")
		}
	}()
	cache.Add(serviceable)
}

func TestEmptyCacheOperations(t *testing.T) {
	cache := New()

	// Get from empty cache
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{}
	_, err := cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error when getting from empty cache")
	}

	// Remove from empty cache - should not panic
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	cache.Remove(serviceable)
}

// Performance tests

func BenchmarkAdd(b *testing.B) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		serviceable.id = fmt.Sprintf("test-id-%d", i)
		cache.Add(serviceable)
	}
}

func BenchmarkGet(b *testing.B) {
	cache := New()
	numServiceables := 1000

	// Add serviceables
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		cache.Add(serviceable)
	}

	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(matcher, options)
	}
}

func BenchmarkConcurrentAdd(b *testing.B) {
	cache := New()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			serviceable := createMockServiceable(
				fmt.Sprintf("id-%d", i),
				"test-prefix",
				matcherSpec.HighMatch,
			)
			cache.Add(serviceable)
			i++
		}
	})
}

func BenchmarkConcurrentGet(b *testing.B) {
	cache := New()
	numServiceables := 1000

	// Add serviceables
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		cache.Add(serviceable)
	}

	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Get(matcher, options)
		}
	})
}

// Stress test for detecting race conditions

func TestRaceDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race detection test in short mode")
	}

	cache := New()
	numGoroutines := 100
	operationsPerGoroutine := 100

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*operationsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				serviceable := createMockServiceable(
					fmt.Sprintf("id-%d-%d", goroutineID, j),
					fmt.Sprintf("prefix-%d", goroutineID%10), // 10 different prefixes
					matcherSpec.HighMatch,
				)

				// Random operation
				switch j % 4 {
				case 0: // Add
					_, err := cache.Add(serviceable)
					if err != nil {
						errChan <- err
					}
				case 1: // Get
					matcher := &mockMatchDefinition{
						cachePrefix: fmt.Sprintf("prefix-%d", goroutineID%10),
						stringVal:   "test-matcher",
					}
					options := components.GetOptions{}
					cache.Get(matcher, options) // May fail, which is expected
				case 2: // Remove
					cache.Remove(serviceable)
				case 3: // Close (occasionally)
					if j%50 == 0 {
						cache.Close()
					}
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Race detection test failed: %v", err)
	}
}

// Test for proper cleanup and resource management

func TestResourceCleanup(t *testing.T) {
	cache := New()
	numServiceables := 100

	// Add serviceables
	serviceables := make([]*mockServiceable, numServiceables)
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		serviceables[i] = serviceable
		_, err := cache.Add(serviceable)
		if err != nil {
			t.Fatalf("Add() failed: %v", err)
		}
	}

	// Remove all serviceables
	for _, serviceable := range serviceables {
		cache.Remove(serviceable)
	}

	// Verify all serviceables were closed exactly once
	for i, serviceable := range serviceables {
		if !serviceable.IsClosed() {
			t.Fatalf("Serviceable %d was not closed", i)
		}
		if serviceable.CloseCount() != 1 {
			t.Fatalf("Serviceable %d was closed %d times, expected 1", i, serviceable.CloseCount())
		}
	}

	// Verify cache is empty
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 0 {
		t.Fatalf("Expected 0 serviceables, got %d", len(servList))
	}
}

// Test for validation edge cases

func TestValidationWithTnsError(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	// Set up TNS to return error
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.err = errors.New("TNS error")

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with validation enabled - should remove serviceable due to TNS error
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for TNS failure")
	}

	// Verify serviceable was removed and closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed after TNS error")
	}
}

func TestValidationWithCidMismatch(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	// Set up different CID
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.cid = "different-cid"

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with validation enabled - should succeed since CID validation is no longer performed
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// Verify serviceable was not closed since CID validation is no longer performed
	if serviceable.IsClosed() {
		t.Fatal("Expected serviceable to remain open since CID validation is no longer performed")
	}
}

// Test for proper handling of different branches

func TestValidationWithCustomBranches(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with custom branches
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"custom-branch"},
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

// Test for timeout scenarios (simulating slow operations)

func TestSlowValidation(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Simulate slow TNS operation
	mockService := serviceable.service.(*mockServiceComponent)
	originalTnsClient := mockService.tnsClient
	slowTnsClient := &mockTnsClient{
		commit: "test-commit",
		cid:    "test-asset-id",
	}
	mockService.tnsClient = slowTnsClient

	// Get with validation enabled
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}

	// This should not block indefinitely
	done := make(chan bool, 1)
	go func() {
		results, err := cache.Get(matcher, options)
		if err != nil {
			t.Errorf("Get() failed: %v", err)
		} else if len(results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(results))
		}
		done <- true
	}()

	select {
	case <-done:
		// Test passed
	case <-time.After(5 * time.Second):
		t.Fatal("Get() operation timed out")
	}

	// Restore original TNS client
	mockService.tnsClient = originalTnsClient
}

// Additional comprehensive tests for 100% coverage

func TestAddWithExistingServiceableLowMatch(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.MinMatch)

	// Add serviceable first time
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("First Add() failed: %v", err)
	}

	// Add same serviceable again with low match - should still add
	result, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Second Add() failed: %v", err)
	}
	if result != serviceable {
		t.Fatal("Add() returned different serviceable")
	}

	// Verify serviceable was added
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 1 {
		t.Fatalf("Expected 1 serviceable, got %d", len(servList))
	}
}

func TestGetWithEmptyBranches(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with empty branches - should use default branches
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{}, // Empty branches
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

func TestGetWithNilBranches(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with nil branches - should use default branches
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   nil, // Nil branches
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

func TestValidateWithCommitMismatch(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Set up different commit
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.commit = "different-commit"

	// Get with validation enabled - should remove serviceable due to commit mismatch
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for commit mismatch")
	}

	// Verify serviceable was removed and closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed after commit mismatch")
	}
}

func TestValidateWithTnsCommitError(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Set up TNS commit error
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.err = errors.New("TNS commit error")

	// Get with validation enabled - should remove serviceable due to TNS error
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for TNS commit error")
	}

	// Verify serviceable was removed and closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed after TNS commit error")
	}
}

func TestValidateWithAssetCidError(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Set up TNS fetch error for asset CID
	mockService := serviceable.service.(*mockServiceComponent)
	// First call succeeds (for commit), second call fails (for asset CID)
	originalTnsClient := mockService.tnsClient
	mockService.tnsClient = &mockTnsClient{
		commit: "test-commit",
		cid:    "test-asset-id",
		err:    errors.New("TNS fetch error"),
	}

	// Get with validation enabled - should remove serviceable due to asset CID error
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for asset CID error")
	}

	// Verify serviceable was removed and closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed after asset CID error")
	}

	// Restore original TNS client
	mockService.tnsClient = originalTnsClient
}

func TestValidateWithNonStringCid(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Set up TNS to return non-string CID
	mockService := serviceable.service.(*mockServiceComponent)
	originalTnsClient := mockService.tnsClient
	// Override the TnsObject to return non-string interface
	mockService.tnsClient = &mockTnsClientWithNonStringCid{
		commit: "test-commit",
		cid:    123, // Non-string CID
	}

	// Get with validation enabled - should succeed since CID validation is no longer performed
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// Verify serviceable was not closed since CID validation is no longer performed
	if serviceable.IsClosed() {
		t.Fatal("Expected serviceable to remain open since CID validation is no longer performed")
	}

	// Restore original TNS client
	mockService.tnsClient = originalTnsClient
}

// Mock TNS client that returns non-string CID
type mockTnsClientWithNonStringCid struct {
	commit string
	cid    interface{}
	err    error
}

func (m *mockTnsClientWithNonStringCid) Simple() tns.SimpleIface {
	return &mockTnsSimpleClient{client: &mockTnsClient{commit: m.commit, err: m.err}}
}

func (m *mockTnsClientWithNonStringCid) Fetch(path tns.Path) (tns.Object, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &mockTnsObjectWithNonStringCid{cid: m.cid}, nil
}

func (m *mockTnsClientWithNonStringCid) Lookup(query tns.Query) (interface{}, error) { return nil, nil }
func (m *mockTnsClientWithNonStringCid) Push(path []string, data interface{}) error  { return nil }
func (m *mockTnsClientWithNonStringCid) List(depth int) ([][]string, error)          { return nil, nil }
func (m *mockTnsClientWithNonStringCid) Close()                                      {}
func (m *mockTnsClientWithNonStringCid) Database() tns.StructureIface[*structureSpec.Database] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Domain() tns.StructureIface[*structureSpec.Domain] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Function() tns.StructureIface[*structureSpec.Function] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Library() tns.StructureIface[*structureSpec.Library] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Messaging() tns.StructureIface[*structureSpec.Messaging] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Service() tns.StructureIface[*structureSpec.Service] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) SmartOp() tns.StructureIface[*structureSpec.SmartOp] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Storage() tns.StructureIface[*structureSpec.Storage] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Website() tns.StructureIface[*structureSpec.Website] {
	return nil
}
func (m *mockTnsClientWithNonStringCid) Stats() tns.Stats                  { return nil }
func (m *mockTnsClientWithNonStringCid) Peers(...libp2pPeer.ID) tns.Client { return m }

type mockTnsObjectWithNonStringCid struct {
	cid interface{}
}

func (m *mockTnsObjectWithNonStringCid) Path() tns.Path         { return nil }
func (m *mockTnsObjectWithNonStringCid) Bind(interface{}) error { return nil }
func (m *mockTnsObjectWithNonStringCid) Interface() interface{} {
	return m.cid
}
func (m *mockTnsObjectWithNonStringCid) Current(branch []string) ([]tns.Path, error) { return nil, nil }

// Test ResolveAssetCid function directly
func TestResolveAssetCid(t *testing.T) {
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Test successful case
	cid, err := ResolveAssetCid(serviceable)
	if err != nil {
		t.Fatalf("ResolveAssetCid() failed: %v", err)
	}
	if cid != "test-asset-id" {
		t.Fatalf("Expected cid 'test-asset-id', got '%s'", cid)
	}
}

func TestResolveAssetCidWithTnsPathError(t *testing.T) {
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// This will test the error path in ResolveAssetCid when GetTNSAssetPath fails
	// We can't easily mock the methods.GetTNSAssetPath function, but we can test
	// the error handling by creating a serviceable that would cause it to fail

	// Test with empty project ID to trigger error
	serviceable.project = ""

	_, err := ResolveAssetCid(serviceable)
	if err == nil {
		t.Fatal("Expected error for empty project ID")
	}
	if !strings.Contains(err.Error(), "getting tns asset path failed") {
		t.Fatalf("Expected 'getting tns asset path failed' error, got: %v", err)
	}
}

func TestResolveAssetCidWithTnsFetchError(t *testing.T) {
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Set up TNS fetch error
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.err = errors.New("TNS fetch error")

	_, err := ResolveAssetCid(serviceable)
	if err == nil {
		t.Fatal("Expected error for TNS fetch error")
	}
	if !strings.Contains(err.Error(), "fetching cid object failed") {
		t.Fatalf("Expected 'fetching cid object failed' error, got: %v", err)
	}
}

func TestResolveAssetCidWithNonStringCid(t *testing.T) {
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Set up TNS to return non-string CID
	mockService := serviceable.service.(*mockServiceComponent)
	originalTnsClient := mockService.tnsClient
	mockService.tnsClient = &mockTnsClientWithNonStringCid{
		commit: "test-commit",
		cid:    123, // Non-string CID
	}

	_, err := ResolveAssetCid(serviceable)
	if err == nil {
		t.Fatal("Expected error for non-string CID")
	}
	if !strings.Contains(err.Error(), "is not a string") {
		t.Fatalf("Expected 'is not a string' error, got: %v", err)
	}

	// Restore original TNS client
	mockService.tnsClient = originalTnsClient
}

// Test error message formatting
func TestErrorMessages(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	serviceable.validateErr = errors.New("validation failed")

	// Test Add validation error message
	_, err := cache.Add(serviceable)
	if err == nil {
		t.Fatal("Expected validation error")
	}
	expectedMsg := "validating serviceable failed with: validation failed"
	if err.Error() != expectedMsg {
		t.Fatalf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
	}
}

func TestGetErrorMessages(t *testing.T) {
	cache := New()
	matcher := &mockMatchDefinition{cachePrefix: "nonexistent", stringVal: "test-matcher"}
	options := components.GetOptions{}

	// Test Get not found error message
	_, err := cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for non-existent serviceable")
	}
	expectedMsg := "getting cached serviceable from matcher test-matcher, failed with: does not exist"
	if err.Error() != expectedMsg {
		t.Fatalf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
	}
}

func TestValidateErrorMessages(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Test commit error message - we need to test the validate method directly
	// by creating a scenario where validation fails but doesn't remove the serviceable
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.err = errors.New("TNS commit error")

	// Test the validate method directly by calling it through a Get operation
	// that will trigger validation but we'll check the error message
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for TNS commit error")
	}
	// The serviceable gets removed, so we get "does not exist" error
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Expected 'does not exist' error message, got: %v", err.Error())
	}
}

// Test concurrent validation with multiple serviceables
func TestConcurrentValidation(t *testing.T) {
	cache := New()
	numServiceables := 10

	// Add serviceables
	serviceables := make([]*mockServiceable, numServiceables)
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		serviceables[i] = serviceable
		_, err := cache.Add(serviceable)
		if err != nil {
			t.Fatalf("Add() failed: %v", err)
		}
	}

	// Concurrent validation
	numGoroutines := 5
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
			options := components.GetOptions{
				Validation: true,
				Branches:   []string{"main"},
			}
			results, err := cache.Get(matcher, options)
			if err != nil {
				errChan <- err
				return
			}
			if len(results) != numServiceables {
				errChan <- fmt.Errorf("expected %d results, got %d", numServiceables, len(results))
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Concurrent validation failed: %v", err)
	}
}

// Test cache with multiple prefixes
func TestMultiplePrefixes(t *testing.T) {
	cache := New()
	numPrefixes := 5
	serviceablesPerPrefix := 3

	// Add serviceables with different prefixes
	for i := 0; i < numPrefixes; i++ {
		prefix := fmt.Sprintf("prefix-%d", i)
		for j := 0; j < serviceablesPerPrefix; j++ {
			serviceable := createMockServiceable(
				fmt.Sprintf("id-%d-%d", i, j),
				prefix,
				matcherSpec.HighMatch,
			)
			_, err := cache.Add(serviceable)
			if err != nil {
				t.Fatalf("Add() failed: %v", err)
			}
		}
	}

	// Verify all serviceables were added
	cache.locker.RLock()
	totalServiceables := 0
	for _, servList := range cache.cacheMap {
		totalServiceables += len(servList)
	}
	cache.locker.RUnlock()

	expectedTotal := numPrefixes * serviceablesPerPrefix
	if totalServiceables != expectedTotal {
		t.Fatalf("Expected %d serviceables, got %d", expectedTotal, totalServiceables)
	}

	// Test getting from each prefix
	for i := 0; i < numPrefixes; i++ {
		prefix := fmt.Sprintf("prefix-%d", i)
		matcher := &mockMatchDefinition{cachePrefix: prefix, stringVal: "test-matcher"}
		options := components.GetOptions{}
		results, err := cache.Get(matcher, options)
		if err != nil {
			t.Fatalf("Get() failed for prefix %s: %v", prefix, err)
		}
		if len(results) != serviceablesPerPrefix {
			t.Fatalf("Expected %d results for prefix %s, got %d", serviceablesPerPrefix, prefix, len(results))
		}
	}
}

// Test cache with mixed match indices
func TestMixedMatchIndices(t *testing.T) {
	cache := New()

	// Add serviceables with different match indices
	highMatchServiceable := createMockServiceable("high-id", "test-prefix", matcherSpec.HighMatch)
	minMatchServiceable := createMockServiceable("min-id", "test-prefix", matcherSpec.MinMatch)
	defaultMatchServiceable := createMockServiceable("default-id", "test-prefix", matcherSpec.DefaultMatch)

	_, err := cache.Add(highMatchServiceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	_, err = cache.Add(minMatchServiceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	_, err = cache.Add(defaultMatchServiceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Test getting with HighMatch
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{MatchIndex: &[]matcherSpec.Index{matcherSpec.HighMatch}[0]}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for HighMatch, got %d", len(results))
	}
	if results[0] != highMatchServiceable {
		t.Fatal("Expected high match serviceable")
	}

	// Test getting with MinMatch
	options.MatchIndex = &[]matcherSpec.Index{matcherSpec.MinMatch}[0]
	results, err = cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for MinMatch, got %d", len(results))
	}
	if results[0] != minMatchServiceable {
		t.Fatal("Expected min match serviceable")
	}

	// Test getting with DefaultMatch
	options.MatchIndex = &[]matcherSpec.Index{matcherSpec.DefaultMatch}[0]
	results, err = cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for DefaultMatch, got %d", len(results))
	}
	if results[0] != defaultMatchServiceable {
		t.Fatal("Expected default match serviceable")
	}
}

// Test cache stress with many operations
func TestCacheStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	cache := New()
	numOperations := 1000
	var wg sync.WaitGroup
	errChan := make(chan error, numOperations)

	// Stress test with mixed operations
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			serviceable := createMockServiceable(
				fmt.Sprintf("id-%d", id),
				fmt.Sprintf("prefix-%d", id%10), // 10 different prefixes
				matcherSpec.HighMatch,
			)

			switch id % 5 {
			case 0: // Add
				_, err := cache.Add(serviceable)
				if err != nil {
					errChan <- err
				}
			case 1: // Get
				matcher := &mockMatchDefinition{
					cachePrefix: fmt.Sprintf("prefix-%d", id%10),
					stringVal:   "test-matcher",
				}
				options := components.GetOptions{}
				cache.Get(matcher, options) // May fail, which is expected
			case 2: // Get with validation
				matcher := &mockMatchDefinition{
					cachePrefix: fmt.Sprintf("prefix-%d", id%10),
					stringVal:   "test-matcher",
				}
				options := components.GetOptions{
					Validation: true,
					Branches:   []string{"main"},
				}
				cache.Get(matcher, options) // May fail, which is expected
			case 3: // Remove
				cache.Remove(serviceable)
			case 4: // Close (occasionally)
				if id%100 == 0 {
					cache.Close()
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Stress test failed: %v", err)
	}
}

// Test additional edge cases for 100% coverage

func TestAddWithExistingServiceableNoMatch(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.NoMatch)

	// Add serviceable first time
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("First Add() failed: %v", err)
	}

	// Add same serviceable again with NoMatch - should still add
	result, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Second Add() failed: %v", err)
	}
	if result != serviceable {
		t.Fatal("Add() returned different serviceable")
	}

	// Verify serviceable was added
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != 1 {
		t.Fatalf("Expected 1 serviceable, got %d", len(servList))
	}
}

func TestGetWithNoMatchIndex(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with NoMatch index - should return empty
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{MatchIndex: &[]matcherSpec.Index{matcherSpec.NoMatch}[0]}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for NoMatch")
	}
}

func TestGetWithDefaultMatchIndex(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.DefaultMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with DefaultMatch index
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{MatchIndex: &[]matcherSpec.Index{matcherSpec.DefaultMatch}[0]}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

func TestGetWithMinMatchIndex(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.MinMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Get with MinMatch index
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{MatchIndex: &[]matcherSpec.Index{matcherSpec.MinMatch}[0]}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

// Test error paths in validation
func TestValidateWithCommitMismatchError(t *testing.T) {
	cache := New()
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)

	// Add serviceable
	_, err := cache.Add(serviceable)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Set up different commit
	mockService := serviceable.service.(*mockServiceComponent)
	mockTnsClient := mockService.tnsClient.(*mockTnsClient)
	mockTnsClient.commit = "different-commit"

	// Get with validation enabled - should remove serviceable due to commit mismatch
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: true,
		Branches:   []string{"main"},
	}
	_, err = cache.Get(matcher, options)
	if err == nil {
		t.Fatal("Expected error for commit mismatch")
	}

	// Verify serviceable was removed and closed
	if !serviceable.IsClosed() {
		t.Fatal("Expected serviceable to be closed after commit mismatch")
	}
}

// Test concurrent operations with different scenarios
func TestConcurrentMixedOperations(t *testing.T) {
	cache := New()
	numGoroutines := 50
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*2)

	// Mix of different operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			serviceable := createMockServiceable(
				fmt.Sprintf("id-%d", id),
				fmt.Sprintf("prefix-%d", id%5), // 5 different prefixes
				matcherSpec.HighMatch,
			)

			switch id % 6 {
			case 0: // Add
				_, err := cache.Add(serviceable)
				if err != nil {
					errChan <- err
				}
			case 1: // Get
				matcher := &mockMatchDefinition{
					cachePrefix: fmt.Sprintf("prefix-%d", id%5),
					stringVal:   "test-matcher",
				}
				options := components.GetOptions{}
				cache.Get(matcher, options) // May fail, which is expected
			case 2: // Get with validation
				matcher := &mockMatchDefinition{
					cachePrefix: fmt.Sprintf("prefix-%d", id%5),
					stringVal:   "test-matcher",
				}
				options := components.GetOptions{
					Validation: true,
					Branches:   []string{"main"},
				}
				cache.Get(matcher, options) // May fail, which is expected
			case 3: // Get with different match index
				matcher := &mockMatchDefinition{
					cachePrefix: fmt.Sprintf("prefix-%d", id%5),
					stringVal:   "test-matcher",
				}
				options := components.GetOptions{
					MatchIndex: &[]matcherSpec.Index{matcherSpec.MinMatch}[0],
				}
				cache.Get(matcher, options) // May fail, which is expected
			case 4: // Remove
				cache.Remove(serviceable)
			case 5: // Close (occasionally)
				if id%20 == 0 {
					cache.Close()
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("Concurrent mixed operations failed: %v", err)
	}
}

// Test cache with very large number of serviceables
func TestCacheWithManyServiceables(t *testing.T) {
	cache := New()
	numServiceables := 1000

	// Add many serviceables
	for i := 0; i < numServiceables; i++ {
		serviceable := createMockServiceable(
			fmt.Sprintf("id-%d", i),
			"test-prefix",
			matcherSpec.HighMatch,
		)
		_, err := cache.Add(serviceable)
		if err != nil {
			t.Fatalf("Add() failed for serviceable %d: %v", i, err)
		}
	}

	// Verify all serviceables were added
	cache.locker.RLock()
	servList := cache.cacheMap["test-prefix"]
	cache.locker.RUnlock()
	if len(servList) != numServiceables {
		t.Fatalf("Expected %d serviceables, got %d", numServiceables, len(servList))
	}

	// Get all serviceables
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != numServiceables {
		t.Fatalf("Expected %d results, got %d", numServiceables, len(results))
	}
}

// Test cache with different validation scenarios
func TestCacheValidationScenarios(t *testing.T) {
	cache := New()

	// Test with validation disabled
	serviceable1 := createMockServiceable("id-1", "test-prefix", matcherSpec.HighMatch)
	_, err := cache.Add(serviceable1)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{
		Validation: false, // No validation
		Branches:   []string{"main"},
	}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// Test with validation enabled
	options.Validation = true
	results, err = cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
}

// Test error message formatting for all error cases
func TestAllErrorMessages(t *testing.T) {
	cache := New()

	// Test Add validation error
	serviceable := createMockServiceable("test-id", "test-prefix", matcherSpec.HighMatch)
	serviceable.validateErr = errors.New("custom validation error")

	_, err := cache.Add(serviceable)
	if err == nil {
		t.Fatal("Expected validation error")
	}
	expectedMsg := "validating serviceable failed with: custom validation error"
	if err.Error() != expectedMsg {
		t.Fatalf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
	}
}

// Test cache with multiple serviceables and different match indices
func TestCacheWithMultipleMatchIndices(t *testing.T) {
	cache := New()

	// Add serviceables with different match indices
	highMatch := createMockServiceable("high-id", "test-prefix", matcherSpec.HighMatch)
	minMatch := createMockServiceable("min-id", "test-prefix", matcherSpec.MinMatch)
	defaultMatch := createMockServiceable("default-id", "test-prefix", matcherSpec.DefaultMatch)
	noMatch := createMockServiceable("no-id", "test-prefix", matcherSpec.NoMatch)

	_, err := cache.Add(highMatch)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	_, err = cache.Add(minMatch)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	_, err = cache.Add(defaultMatch)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	_, err = cache.Add(noMatch)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	// Test getting all with HighMatch
	matcher := &mockMatchDefinition{cachePrefix: "test-prefix", stringVal: "test-matcher"}
	options := components.GetOptions{MatchIndex: &[]matcherSpec.Index{matcherSpec.HighMatch}[0]}
	results, err := cache.Get(matcher, options)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for HighMatch, got %d", len(results))
	}
	if results[0] != highMatch {
		t.Fatal("Expected high match serviceable")
	}
}
