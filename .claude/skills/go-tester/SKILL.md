---
name: go-tester
description: |
  Go testing expertise covering table-driven tests, mocking, benchmarks, and test organization.
  Use when: (1) Writing new tests for Go code, (2) Reviewing or improving existing tests,
  (3) Setting up mocks for external dependencies, (4) Writing benchmarks for performance testing,
  (5) Debugging flaky or failing tests, (6) Questions about Go testing patterns or best practices.
---

# Go Testing Guide

## Table-Driven Tests

Use slice of structs pattern with `t.Run` for subtests:

```go
func TestProcess(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    int
        wantErr bool
    }{
        {"valid input", "hello", 5, false},
        {"empty input", "", 0, true},
        {"unicode", "日本語", 3, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Process(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Process() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("Process() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

### When NOT to use table-driven tests
- Single test case with complex setup
- Tests requiring significantly different assertions
- Integration tests with unique state management

## Interface-Based Mocking

Define interfaces for dependencies, create mock implementations:

```go
// In production code - define interface for what you need
type ProcessLister interface {
    Processes() ([]Process, error)
}

// In test file - create mock
type mockProcessLister struct {
    processes []Process
    err       error
}

func (m *mockProcessLister) Processes() ([]Process, error) {
    return m.processes, m.err
}

// Test using mock
func TestDiscovery(t *testing.T) {
    mock := &mockProcessLister{
        processes: []Process{{PID: 123, Name: "java"}},
    }
    d := NewDiscoverer(mock)
    // ...
}
```

## Test Helpers

Use `t.Helper()` for cleaner stack traces:

```go
func assertNoError(t *testing.T, err error) {
    t.Helper()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}

func assertContains(t *testing.T, slice []string, want string) {
    t.Helper()
    for _, s := range slice {
        if s == want {
            return
        }
    }
    t.Errorf("slice %v does not contain %q", slice, want)
}
```

## Parallel Tests

Use `t.Parallel()` for independent tests:

```go
func TestParallel(t *testing.T) {
    tests := []struct {
        name  string
        input int
    }{
        {"case1", 1},
        {"case2", 2},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel() // Mark subtest as parallel
            // Test logic - must not share mutable state
        })
    }
}
```

## Benchmarks

```go
func BenchmarkProcess(b *testing.B) {
    input := setupTestData()
    b.ResetTimer() // Exclude setup from timing

    for i := 0; i < b.N; i++ {
        Process(input)
    }
}

// With sub-benchmarks for different sizes
func BenchmarkProcessSizes(b *testing.B) {
    sizes := []int{10, 100, 1000}
    for _, size := range sizes {
        b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
            input := make([]byte, size)
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                Process(input)
            }
        })
    }
}
```

Run: `go test -bench=. -benchmem`

## Testing with Context

```go
func TestWithTimeout(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    result, err := SlowOperation(ctx)
    if err != context.DeadlineExceeded {
        t.Errorf("expected timeout, got: %v", err)
    }
}
```

## Test File Organization

- `foo_test.go` alongside `foo.go`
- Use `_test` package suffix for black-box testing: `package foo_test`
- Use same package for white-box testing when needed: `package foo`
- `testdata/` directory for test fixtures (ignored by go build)

## Common Commands

```bash
go test ./...                           # Run all tests
go test -v ./pkg/discovery              # Verbose, specific package
go test -run TestName ./...             # Run specific test
go test -run TestName/subtest ./...     # Run specific subtest
go test -count=1 ./...                  # Disable test caching
go test -race ./...                     # Enable race detector
go test -cover ./...                    # Show coverage
go test -coverprofile=c.out ./...       # Generate coverage profile
go tool cover -html=c.out               # View coverage in browser
```

## Edge Cases to Always Test

- nil/zero values
- Empty slices/maps
- Context cancellation
- Permission errors (for this codebase)
- Container vs host processes (for this codebase)
