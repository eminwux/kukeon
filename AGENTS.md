# Testing Guidelines for AI Agents

This document establishes the **mandatory testing pattern** for all command implementations in the `cmd/kuke/` tree. All commands MUST use the **context-based mocking pattern** described below.

## Core Principle: Context-Based Mocking

All commands that interact with controllers MUST use context-based dependency injection for testing. This pattern allows tests to inject mock controllers via the command's context, enabling isolated unit testing without requiring real infrastructure.

## Implementation Pattern

### 1. Define Controller Interface

Each command MUST define a local interface that describes the controller methods it needs:

```go
type resourceController interface {
    CreateResource(opts controller.CreateResourceOptions) (controller.CreateResourceResult, error)
    // ... other methods
}
```

### 2. Define Mock Controller Key

Each command MUST define a unique key type for context injection:

```go
// mockControllerKey is used to inject mock controllers in tests via context
type mockControllerKey struct{}
```

**Important:** This type MUST be defined in the implementation file (`.go`), NOT in the test file.

### 3. Implement Context-Based Controller Retrieval

In the `RunE` function (or equivalent), check the context for a mock controller first:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    var ctrl resourceController
    if mockCtrl, ok := cmd.Context().Value(mockControllerKey{}).(resourceController); ok {
        ctrl = mockCtrl
    } else {
        realCtrl, err := shared.ControllerFromCmd(cmd)
        if err != nil {
            return err
        }
        ctrl = &controllerWrapper{ctrl: realCtrl}
    }

    // Use ctrl for operations...
}
```

### 4. Create Controller Wrapper (If Needed)

If the real controller (`*controller.Exec`) doesn't match the interface exactly, create a wrapper:

```go
type controllerWrapper struct {
    ctrl *controller.Exec
}

func (w *controllerWrapper) CreateResource(opts controller.CreateResourceOptions) (controller.CreateResourceResult, error) {
    return w.ctrl.CreateResource(opts)
}
```

## Test Implementation Pattern

### 1. Create Fake Controller

In the test file, define a fake controller that implements the interface:

```go
type fakeResourceController struct {
    createResourceFn func(opts controller.CreateResourceOptions) (controller.CreateResourceResult, error)
}

func (f *fakeResourceController) CreateResource(opts controller.CreateResourceOptions) (controller.CreateResourceResult, error) {
    if f.createResourceFn == nil {
        return controller.CreateResourceResult{}, errors.New("unexpected CreateResource call")
    }
    return f.createResourceFn(opts)
}
```

### 2. Set Up Context with Mock

In each test case, inject the mock controller via context:

```go
func TestNewResourceCmd(t *testing.T) {
    tests := []struct {
        name       string
        controller resourceController
        wantErr    string
        // ... other fields
    }{
        {
            name: "success case",
            controller: &fakeResourceController{
                createResourceFn: func(opts controller.CreateResourceOptions) (controller.CreateResourceResult, error) {
                    // Test implementation
                    return controller.CreateResourceResult{}, nil
                },
            },
        },
        // ... more test cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            cmd := NewResourceCmd()
            cmd.SetOut(&bytes.Buffer{})
            cmd.SetErr(&bytes.Buffer{})

            // Set up context with logger
            logger := slog.New(slog.NewTextHandler(io.Discard, nil))
            ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

            // Inject mock controller via context if provided
            if tt.controller != nil {
                ctx = context.WithValue(ctx, mockControllerKey{}, tt.controller)
            }
            cmd.SetContext(ctx)

            cmd.SetArgs(tt.args)
            err := cmd.Execute()

            // Assertions...
        })
    }
}
```

### 3. Package Naming for Test Files

**CRITICAL:** All test files MUST use the `_test` suffix in their package declaration.

**CORRECT:**

```go
package cell_test  // NOT package cell
```

**Why this matters:**

- Enforces testing of the public API only
- Prevents access to unexported (lowercase) symbols
- Follows Go best practices for external testing
- Makes it clear that tests are in a separate package

When using `package packagename_test`, you MUST import the package being tested:

```go
import (
    "context"
    "io"
    "log/slog"
    // ... other imports

    "github.com/eminwux/kukeon/cmd/types"
    cell "github.com/eminwux/kukeon/cmd/kuke/delete/cell"  // Import the package being tested
    // ... other imports
)
```

Then reference exported types/functions with the package prefix:

```go
cmd := cell.NewCellCmd()  // Use package prefix
```

### 4. Required Imports for Tests

Test files MUST include these imports:

```go
import (
    "context"
    "io"
    "log/slog"
    // ... other imports

    "github.com/eminwux/kukeon/cmd/types"
    packagename "github.com/eminwux/kukeon/cmd/kuke/.../packagename"  // Import package being tested
    // ... other imports
)
```

**Note:** When using `package packagename_test`, you MUST import the package being tested to access its exported functions and types.

## What NOT to Do

### ❌ DO NOT Use Global Variable Stubbing

**BAD:**

```go
var controllerFactory = func(cmd *cobra.Command) (resourceController, error) {
    return shared.ControllerFromCmd(cmd)
}

// In test:
controllerFactory = func(*cobra.Command) (resourceController, error) {
    return mockController, nil
}
```

### ❌ DO NOT Replace RunE Entirely

**BAD:**

```go
// In test:
cmd.RunE = func(cmd *cobra.Command, args []string) error {
    // Completely different implementation
    return testImplementation(cmd, args)
}
```

### ❌ DO NOT Define mockControllerKey in Test Files

**BAD:**

```go
// In _test.go file:
type mockControllerKey struct{}  // WRONG - should be in .go file
```

### ❌ DO NOT Skip Context Setup

**BAD:**

```go
// Missing context setup:
cmd := NewResourceCmd()
err := cmd.Execute()  // Will fail without logger in context
```

### ❌ DO NOT Use Same Package Name in Test Files

**BAD:**

```go
// In cell_test.go:
package cell  // WRONG - should be package cell_test

// This allows access to unexported symbols, which defeats the purpose of testing
```

**GOOD:**

```go
// In cell_test.go:
package cell_test  // CORRECT

import (
    cell "github.com/eminwux/kukeon/cmd/kuke/delete/cell"
    // ... other imports
)

// Now you can only access exported (capitalized) symbols:
cmd := cell.NewCellCmd()  // Correct - NewCellCmd is exported
```

## Examples from Codebase

### Reference Implementation: `cmd/kuke/delete/cell/cell.go`

This file demonstrates the canonical pattern:

- Interface definition: `cellController`
- Mock key: `mockControllerKey`
- Context-based retrieval in `RunE`
- Wrapper for real controller

### Reference Test: `cmd/kuke/delete/cell/cell_test.go`

This file demonstrates:

- Fake controller implementation
- Context setup with logger
- Mock injection via context
- Proper test structure

## Special Cases

### Commands with Real Filesystem Operations

Some commands (e.g., `cmd/kuke/get/realm`) may use real filesystem operations in tests. The context-based mocking pattern still applies - if no mock is provided, the real controller is used. This allows both:

- Unit tests with mocks (fast, isolated)
- Integration tests with real controllers (slower, more realistic)

### Commands with Additional Dependencies

If a command has dependencies beyond the controller (e.g., printer functions), you may still stub those via global variables or function parameters, but the **controller MUST use context-based injection**.

## Verification Checklist

Before submitting code, verify:

- [ ] Controller interface is defined in the implementation file
- [ ] `mockControllerKey` is defined in the implementation file (not test file)
- [ ] `RunE` checks context for mock controller first
- [ ] `controllerWrapper` exists if needed to adapt real controller
- [ ] Test files use `package packagename_test` (not `package packagename`)
- [ ] Test files import the package being tested when using `_test` package
- [ ] Tests create fake controller implementing the interface
- [ ] Tests set up context with logger (`types.CtxLogger`)
- [ ] Tests inject mock controller via `mockControllerKey` in context
- [ ] Tests use `cmd.Execute()` (not direct `RunE` calls, unless necessary)
- [ ] No global variable stubbing for controllers
- [ ] All tests pass

## Consistency Across Command Trees

This pattern is **mandatory** and has been standardized across:

- `cmd/kuke/delete/` - All delete commands
- `cmd/kuke/create/` - All create commands
- `cmd/kuke/get/` - All get commands

Any new commands or modifications to existing commands MUST follow this pattern.

## Questions?

If you're unsure about the pattern, refer to:

1. `cmd/kuke/delete/cell/` - Simplest example
2. `cmd/kuke/create/container/` - Example with wrapper
3. `cmd/kuke/get/stack/` - Example with multiple methods

All commands in these directories follow the exact same pattern.
