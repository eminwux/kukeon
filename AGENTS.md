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

---

# Autocomplete Guidelines for AI Agents

This document establishes the **mandatory autocomplete pattern** for all command implementations in the `cmd/kuke/` tree. All commands that accept resource names or related values MUST provide shell completion using the patterns described below.

**Exception:** The `create realm` command is the only command that MUST NOT have autocomplete for its positional argument (see Special Cases section).

## Core Principle: Reusable Completion Functions

All completion functions MUST be placed in the `cmd/config/autocomplete.go` package for reusability across commands. This ensures consistency and allows multiple commands to share the same completion logic. Functions MUST follow Cobra's completion function signature, handle errors gracefully, and support prefix filtering.

## Implementation Pattern

### 1. Function Signature

All completion functions MUST follow Cobra's standard completion function signature:

```go
func CompleteResourceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)
```

The function parameters are:
- `cmd`: The Cobra command being completed
- `args`: Already provided arguments
- `toComplete`: The prefix string the user has typed (for filtering)

The return values are:
- `[]string`: List of completion suggestions
- `cobra.ShellCompDirective`: Directive controlling completion behavior (typically `cobra.ShellCompDirectiveNoFileComp`)

### 2. Controller Access

Completion functions MUST use the `controllerFromCmd` helper function (defined in `cmd/config/autocomplete.go`) to access the controller. This helper duplicates controller creation logic to avoid import cycles with shared packages:

```go
func CompleteRealmNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    ctrl, err := controllerFromCmd(cmd)
    if err != nil {
        // Return empty completion on error (controller unavailable)
        return []string{}, cobra.ShellCompDirectiveNoFileComp
    }

    realms, err := ctrl.ListRealms()
    if err != nil {
        // Return empty completion on error
        return []string{}, cobra.ShellCompDirectiveNoFileComp
    }

    // Filter and return results...
}
```

### 3. Error Handling

Completion functions MUST handle errors gracefully by returning empty completion lists rather than failing:

```go
if err != nil {
    // Return empty completion on error
    return []string{}, cobra.ShellCompDirectiveNoFileComp
}
```

This ensures that autocomplete failures don't break the shell completion system.

### 4. Prefix Filtering

Completion functions MUST filter results by the `toComplete` prefix when provided:

```go
names := make([]string, 0, len(realms))
for _, realm := range realms {
    name := realm.Metadata.Name
    // Filter by toComplete prefix if provided
    if toComplete == "" || strings.HasPrefix(name, toComplete) {
        names = append(names, name)
    }
}
```

### 5. Shell Completion Directive

Completion functions MUST return `cobra.ShellCompDirectiveNoFileComp` to prevent file system completion from interfering with resource name completion:

```go
return names, cobra.ShellCompDirectiveNoFileComp
```

### 6. Function Naming

Completion functions MUST be exported (capitalized) and follow the naming pattern `Complete<ResourceType>Names`:

```go
// Good:
func CompleteRealmNames(...)
func CompleteSpaceNames(...)
func CompleteStackNames(...)

// Bad:
func completeRealmNames(...)  // Not exported
func GetRealmCompletions(...)  // Wrong naming pattern
```

## Registration Pattern

### 1. For Positional Arguments

For commands that accept resource names as positional arguments, register the completion function using `ValidArgsFunction`:

```go
func NewRealmCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:  "realm [name]",
        // ... other fields
    }

    // Register autocomplete for positional argument
    cmd.ValidArgsFunction = config.CompleteRealmNames

    return cmd
}
```

### 2. For Flags

For commands that accept resource names via flags, register the completion function using `RegisterFlagCompletionFunc`:

```go
func NewSpaceCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:  "space [name]",
        // ... other fields
    }

    cmd.Flags().String("realm", "", "Realm that will own the space")
    _ = viper.BindPFlag(config.KUKE_CREATE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

    // Register autocomplete function for --realm flag
    _ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

    return cmd
}
```

**Important:** Register completion functions AFTER flag definitions and BEFORE returning the command.

## Test Implementation Pattern

### 1. Test File Location

Tests for completion functions MUST be in `cmd/config/autocomplete_test.go` using `package config_test`:

```go
package config_test

import (
    "github.com/eminwux/kukeon/cmd/config"
    // ... other imports
)
```

### 2. Test Structure

Tests MUST use real filesystem operations with temporary directories to create test data:

```go
func TestCompleteRealmNames(t *testing.T) {
    tests := []struct {
        name        string
        setup       func(t *testing.T, runPath string)
        toComplete   string
        wantNames   []string
        noLogger    bool
    }{
        {
            name: "success with multiple realms",
            setup: func(t *testing.T, runPath string) {
                createRealmMetadata(t, runPath, "alpha", "alpha-ns")
                createRealmMetadata(t, runPath, "bravo", "bravo-ns")
            },
            toComplete: "",
            wantNames:  []string{"alpha", "bravo"},
        },
        // ... more test cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            runPath := t.TempDir()
            cmd := setupTestCommand(t, runPath, tt.noLogger)

            if tt.setup != nil {
                tt.setup(t, runPath)
            }

            names, directive := config.CompleteRealmNames(cmd, []string{}, tt.toComplete)

            // Assertions...
        })
    }
}
```

### 3. Test Scenarios

Tests MUST cover:
- Success cases with multiple resources
- Empty list scenarios
- Prefix filtering
- Error handling (e.g., logger not in context)
- Controller errors

### 4. Command Registration Tests

**MANDATORY:** All commands that register autocomplete functions MUST include tests to verify the registration. This ensures that autocomplete functionality is properly wired up and prevents regressions.

In command test files, add tests to verify completion functions are properly registered:

```go
func TestNewSpaceCmd_AutocompleteRegistration(t *testing.T) {
    cmd := space.NewSpaceCmd()

    // Test that realm flag exists
    realmFlag := cmd.Flags().Lookup("realm")
    if realmFlag == nil {
        t.Fatal("expected 'realm' flag to exist")
    }

    // Verify flag structure (completion function registration is verified by Cobra)
    if realmFlag.Usage != "Realm that will own the space" {
        t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
    }
}
```

## What NOT to Do

### ❌ DO NOT Create Completion Functions in Command Packages

**BAD:**

```go
// In cmd/kuke/create/realm/realm.go:
func completeRealmNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    // Completion logic here
}
```

**GOOD:**

```go
// In cmd/config/autocomplete.go:
func CompleteRealmNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    // Completion logic here
}

// In cmd/kuke/create/realm/realm.go:
cmd.ValidArgsFunction = config.CompleteRealmNames
```

### ❌ DO NOT Skip Error Handling

**BAD:**

```go
func CompleteRealmNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    ctrl, _ := controllerFromCmd(cmd)  // Ignoring error
    realms, _ := ctrl.ListRealms()     // Ignoring error
    // ...
}
```

**GOOD:**

```go
func CompleteRealmNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    ctrl, err := controllerFromCmd(cmd)
    if err != nil {
        return []string{}, cobra.ShellCompDirectiveNoFileComp
    }

    realms, err := ctrl.ListRealms()
    if err != nil {
        return []string{}, cobra.ShellCompDirectiveNoFileComp
    }
    // ...
}
```

### ❌ DO NOT Forget Prefix Filtering

**BAD:**

```go
names := make([]string, 0, len(realms))
for _, realm := range realms {
    names = append(names, realm.Metadata.Name)  // Not filtering by toComplete
}
```

**GOOD:**

```go
names := make([]string, 0, len(realms))
for _, realm := range realms {
    name := realm.Metadata.Name
    if toComplete == "" || strings.HasPrefix(name, toComplete) {
        names = append(names, name)
    }
}
```

### ❌ DO NOT Use File Completion Directives

**BAD:**

```go
return names, cobra.ShellCompDirectiveDefault  // Allows file completion
```

**GOOD:**

```go
return names, cobra.ShellCompDirectiveNoFileComp  // Prevents file completion
```

### ❌ DO NOT Register Before Flag Definition

**BAD:**

```go
func NewSpaceCmd() *cobra.Command {
    cmd := &cobra.Command{...}

    // Registering before flag exists
    _ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

    cmd.Flags().String("realm", "", "Realm that will own the space")
    return cmd
}
```

**GOOD:**

```go
func NewSpaceCmd() *cobra.Command {
    cmd := &cobra.Command{...}

    cmd.Flags().String("realm", "", "Realm that will own the space")
    _ = viper.BindPFlag(config.KUKE_CREATE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

    // Register after flag definition
    _ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

    return cmd
}
```

## Examples from Codebase

### Reference Implementation: `cmd/config/autocomplete.go`

This file demonstrates the canonical pattern:

- Function signature matching Cobra's requirements
- Controller access via `controllerFromCmd` helper
- Error handling with graceful fallback
- Prefix filtering support
- Proper shell completion directive

### Reference Registration: `cmd/kuke/create/space/space.go`

This file demonstrates:

- Flag completion registration
- Proper placement after flag definition
- Import of config package for completion functions

### Reference Test: `cmd/config/autocomplete_test.go`

This file demonstrates:

- Test structure with table-driven tests
- Real filesystem operations with temporary directories
- Multiple test scenarios (success, empty, prefix filtering, errors)
- Proper test setup and teardown

## Special Cases

### Create Realm Command Exception

**IMPORTANT:** The `create realm` command is the **only exception** to the autocomplete requirement. It MUST NOT have autocomplete for the positional argument.

**Why:** The create realm command is used to create new realms, so there are no existing realms to autocomplete. Users should type the realm name explicitly.

**Implementation:**
- Do NOT register `ValidArgsFunction` for the create realm command
- The test `TestNewRealmCmd_AutocompleteRegistration` should verify that `ValidArgsFunction` is `nil`

```go
// In cmd/kuke/create/realm/realm.go:
func NewRealmCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:  "realm [name]",
        // ... other fields
    }

    // DO NOT register ValidArgsFunction - create realm is the only exception
    // cmd.ValidArgsFunction = config.CompleteRealmNames  // ❌ DO NOT DO THIS

    return cmd
}
```

```go
// In cmd/kuke/create/realm/realm_test.go:
func TestNewRealmCmd_AutocompleteRegistration(t *testing.T) {
    cmd := realm.NewRealmCmd()

    // Test that ValidArgsFunction is NOT set (create realm is the only exception)
    if cmd.ValidArgsFunction != nil {
        t.Fatal("expected ValidArgsFunction to be nil for create realm command")
    }
}
```

### Commands with Multiple Resource Types

If a command needs completion for multiple resource types, create separate functions for each:

```go
// In cmd/config/autocomplete.go:
func CompleteRealmNames(...)
func CompleteSpaceNames(...)
func CompleteStackNames(...)

// In command:
cmd.ValidArgsFunction = config.CompleteRealmNames
_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
```

### Commands with Derived Completions

Some completions may depend on other command arguments. In these cases, the completion function can access already-provided arguments via the `args` parameter:

```go
func CompleteSpacesInRealm(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    // Extract realm from args or flags
    realm := extractRealmFromArgsOrFlags(cmd, args)
    if realm == "" {
        return []string{}, cobra.ShellCompDirectiveNoFileComp
    }

    // List spaces filtered by realm
    // ...
}
```

### Parent Commands with Subcommand Completion

For parent commands that need to provide autocomplete for their subcommand names (e.g., `kuke get <TAB>`), the completion function MUST be defined in the command file itself as a private function or inline, NOT in `autocomplete.go`.

**Why:** Subcommand lists are static and command-specific, unlike resource name completions which are dynamic and reusable across commands.

**GOOD:**

```go
// In cmd/kuke/get/get.go:
func NewGetCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "get",
        // ...
    }

    cmd.ValidArgsFunction = completeGetSubcommands

    cmd.AddCommand(
        realmcmd.NewRealmCmd(),
        // ... other subcommands
    )

    return cmd
}

// completeGetSubcommands provides shell completion for get subcommand names.
func completeGetSubcommands(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    subcommands := []string{"realm", "space", "stack", "cell", "container"}

    if toComplete == "" {
        return subcommands, cobra.ShellCompDirectiveNoFileComp
    }

    matches := make([]string, 0, len(subcommands))
    for _, subcmd := range subcommands {
        if strings.HasPrefix(subcmd, toComplete) {
            matches = append(matches, subcmd)
        }
    }

    return matches, cobra.ShellCompDirectiveNoFileComp
}
```

**BAD:**

```go
// In cmd/config/autocomplete.go:
func CompleteGetSubcommands(...) {  // WRONG - should not be in autocomplete.go
    // ...
}

// In cmd/kuke/get/get.go:
cmd.ValidArgsFunction = config.CompleteGetSubcommands  // WRONG
```

**Alternative (inline):**

```go
cmd.ValidArgsFunction = func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    subcommands := []string{"realm", "space", "stack", "cell", "container"}
    // ... filtering logic
    return matches, cobra.ShellCompDirectiveNoFileComp
}
```

## Verification Checklist

Before submitting code, verify:

- [ ] Completion function is in `cmd/config/autocomplete.go` (not in command package) - **EXCEPT** for subcommand completion functions which MUST be in the command file
- [ ] Function follows Cobra's completion signature
- [ ] Function handles errors gracefully (returns empty on error)
- [ ] Function filters by `toComplete` prefix when provided
- [ ] Function returns `cobra.ShellCompDirectiveNoFileComp`
- [ ] Function is exported with proper naming (`Complete<Resource>Names`) - **EXCEPT** subcommand completion functions which should be private
- [ ] Completion is registered using `ValidArgsFunction` (positional) or `RegisterFlagCompletionFunc` (flags)
- [ ] Registration happens after flag definition and before returning command
- [ ] Tests exist in `cmd/config/autocomplete_test.go` using `package config_test` (for functions in autocomplete.go)
- [ ] Tests cover success, empty list, prefix filtering, and error scenarios
- [ ] **MANDATORY:** Command test file includes `TestNew<Command>Cmd_AutocompleteRegistration` test to verify completion registration
- [ ] All tests pass

## Consistency Across Command Trees

This pattern is **mandatory** and should be applied to:

- `cmd/kuke/create/` - All create commands with resource name arguments/flags
- `cmd/kuke/get/` - All get commands with resource name arguments/flags
- `cmd/kuke/delete/` - All delete commands with resource name arguments/flags
- Any other commands that accept resource names

Any new commands or modifications to existing commands that accept resource names MUST follow this pattern.

## Questions?

If you're unsure about the pattern, refer to:

1. `cmd/config/autocomplete.go` - Function implementation examples
2. `cmd/kuke/create/space/space.go` - Flag completion registration
3. `cmd/config/autocomplete_test.go` - Test patterns

All completion functions in these files follow the exact same pattern.

---

# Test Consistency and Completeness Verification

This document establishes the **mandatory process** for verifying test consistency and completeness across all cobra commands in the `cmd/` subtree. This process ensures that all commands follow the same testing patterns and have equivalent test coverage.

## Purpose

Regular verification of test consistency helps:
- Identify missing tests across command groups
- Ensure all commands follow the same testing patterns
- Maintain consistent test coverage across similar command types
- Prevent regressions in test quality

## Process Overview

The verification process consists of six systematic steps:

1. **Catalog Commands** - Identify all command files and their corresponding test files
2. **Extract Test Functions** - Extract and categorize all test function names from test files
3. **Build Comparison Matrix** - Create matrices showing which test types each command has
4. **Identify Patterns** - Determine which commands follow consistent patterns and which are outliers
5. **Group by Consistency** - Organize commands into consistency groups
6. **Generate Report** - Create a comprehensive report with findings and recommendations

## Step-by-Step Process

### Step 1: Catalog All Commands and Test Files

**Objective:** Create a complete inventory of all command files and their corresponding test files.

**Actions:**
1. Search for all command constructor functions:
   ```bash
   grep -r "^func New.*Cmd(" cmd/kuke --include="*.go" | grep -v "_test.go"
   ```

2. Search for all test files:
   ```bash
   find cmd/kuke -name "*_test.go" -type f
   ```

3. Verify each command file has a corresponding test file:
   - For each `New<Command>Cmd()` function found, verify a `*_test.go` file exists in the same directory
   - Note any commands without test files

**Expected Output:**
- List of all command files (e.g., `cmd/kuke/create/realm/realm.go`)
- List of all test files (e.g., `cmd/kuke/create/realm/realm_test.go`)
- Mapping between commands and their test files

### Step 2: Extract Test Functions

**Objective:** Extract and categorize all test function names from each test file.

**Actions:**
1. Search for all test functions:
   ```bash
   grep -r "^func Test" cmd/kuke --include="*_test.go"
   ```

2. For each test file, categorize test functions by type:
   - **Command Structure Tests**: `TestNew<Command>Cmd` or `TestNew<Command>CmdMetadata`
   - **Command Execution Tests**: `TestNew<Command>CmdRunE`
   - **Output Formatting Tests**: `TestPrint<Resource>Result` or `TestPrint<Resource>`
   - **Autocomplete Registration Tests**: `TestNew<Command>Cmd_AutocompleteRegistration`
   - **Subcommand Registration Tests**: `TestNew<Command>CmdRegistersSubcommands`
   - **Persistent Flags Tests**: `TestNew<Command>CmdPersistentFlags`
   - **Viper Binding Tests**: `TestNew<Command>CmdViperBindings`
   - **Other Tests**: Helper tests, utility tests, etc.

**Expected Output:**
- Complete list of test functions per file
- Categorization of each test function by type
- Identification of test function naming patterns

### Step 3: Build Comparison Matrix

**Objective:** Create matrices showing which test types each command has or is missing.

**Actions:**
1. **For Parent Commands** (create, delete, get, start, stop, purge, kill):
   - Create a matrix with columns: Command, Metadata, Subcommands, Autocomplete, Persistent Flags, Viper Bindings
   - Mark each cell as ✅ (present) or ❌ (missing)

2. **For Resource Commands** (realm, space, stack, cell, container):
   - Create separate matrices for each operation (create, delete, get, start, stop, purge)
   - Columns vary by operation but typically include: Structure, RunE, PrintResult/Print, Autocomplete
   - Mark each cell as ✅ (present) or ❌ (missing)

3. **For Special Commands** (init, version, autocomplete):
   - Document their unique test patterns separately
   - Note that they may have different expected test types

**Expected Output:**
- Parent commands test coverage matrix
- Resource commands test coverage matrices (one per operation)
- Special commands documentation

### Step 4: Identify Patterns

**Objective:** Determine which commands follow consistent patterns and identify outliers.

**Actions:**
1. **Group Commands by Type:**
   - Parent commands (create, delete, get, start, stop, purge, kill)
   - Resource commands by operation (create/realm, delete/realm, get/realm, etc.)
   - Special commands (init, version, autocomplete)

2. **Identify Consistent Patterns:**
   - Commands that have identical test structures
   - Commands that follow the same naming conventions
   - Commands that have the same test types

3. **Identify Inconsistent Patterns:**
   - Commands missing expected test types
   - Commands with different naming conventions
   - Commands with unusual test structures

**Expected Output:**
- List of consistent patterns (e.g., "All start/stop resource commands have identical test patterns")
- List of inconsistent patterns (e.g., "Some get commands missing execution tests")
- Identification of outliers

### Step 5: Group by Consistency

**Objective:** Organize commands into consistency groups based on test coverage completeness.

**Actions:**
1. **Group 1: Fully Consistent Commands**
   - Commands that have all expected test types for their category
   - Mark as "Complete" in the matrix

2. **Group 2: Partially Consistent Commands**
   - Commands missing one or more expected test types
   - Mark as "Partial" in the matrix
   - Document which tests are missing

3. **Group 3: Inconsistent Commands**
   - Commands with unusual test patterns or missing critical tests
   - Mark as "Incomplete" in the matrix
   - Document specific issues

4. **Group 4: Special Cases**
   - Commands with unique requirements (init, version, autocomplete)
   - Document their unique patterns and why they differ

**Expected Output:**
- Commands grouped by consistency level
- Summary of what makes each group consistent or inconsistent
- Count of commands in each group

### Step 6: Generate Report

**Objective:** Create a comprehensive report documenting findings and recommendations.

**Actions:**
1. **Create Summary Section:**
   - Overall assessment of test coverage
   - Statistics (e.g., "X% of commands have complete coverage")
   - High-level findings

2. **Create Detailed Analysis Section:**
   - For each command group, document:
     - Which test types are present
     - Which test types are missing
     - Any unusual patterns

3. **Create Recommendations Section:**
   - **High Priority:** Missing critical tests (e.g., autocomplete, execution tests)
   - **Medium Priority:** Missing important tests (e.g., structure tests)
   - **Low Priority:** Naming/style inconsistencies

4. **Create Summary Matrices:**
   - Include the comparison matrices from Step 3
   - Add status column (Complete/Partial/Incomplete)

5. **Create Consistency Patterns Section:**
   - Document consistent patterns found
   - Document inconsistent patterns found
   - Note any naming/style differences

**Expected Output:**
- Comprehensive markdown report (e.g., `TESTING_CONSISTENCY_REPORT.md`)
- Clear recommendations prioritized by importance
- Actionable items for improving test consistency

## Expected Test Types by Command Category

### Parent Commands

All parent commands (create, delete, get, start, stop, purge) should have:
- ✅ `TestNew<Command>CmdMetadata` or `TestNew<Command>Cmd` (structure test)
- ✅ `TestNew<Command>CmdRegistersSubcommands` (subcommand registration)
- ✅ `TestNew<Command>Cmd_AutocompleteRegistration` (autocomplete for subcommands)
- ✅ `TestNew<Command>CmdPersistentFlags` (if persistent flags exist)
- ✅ `TestNew<Command>CmdViperBindings` (if persistent flags exist)

**Exception:** `kill` command doesn't have subcommands (handles multiple resource types in one command).

### Resource Commands - Create

All create resource commands should have:
- ✅ `TestNew<Resource>Cmd` or `TestNew<Resource>CmdRunE` (structure/execution)
- ✅ `TestNew<Resource>CmdRunE` (execution test)
- ✅ `TestPrint<Resource>Result` (output formatting)
- ✅ `TestNew<Resource>Cmd_AutocompleteRegistration` (autocomplete)

**Exception:** `create realm` command's autocomplete test should verify that `ValidArgsFunction` is `nil` (create realm is the only command without autocomplete).

### Resource Commands - Delete

All delete resource commands should have:
- ✅ `TestNew<Resource>Cmd` (structure test, optional)
- ✅ `TestNew<Resource>CmdRunE` (execution test)
- ✅ `TestNew<Resource>Cmd_AutocompleteRegistration` (autocomplete)

### Resource Commands - Get

All get resource commands should have:
- ✅ `TestNew<Resource>Cmd` (structure test, optional)
- ✅ `TestNew<Resource>CmdRunE` (execution test)
- ✅ `TestPrint<Resource>` and/or `TestPrint<Resources>` (output formatting)
- ✅ `TestNew<Resource>Cmd_AutocompleteRegistration` (autocomplete)

### Resource Commands - Start/Stop

All start/stop resource commands should have:
- ✅ `TestNew<Resource>CmdRunE` (execution test)
- ✅ `TestNew<Resource>Cmd_AutocompleteRegistration` (autocomplete)

### Resource Commands - Purge

All purge resource commands should have:
- ✅ `TestNew<Resource>Cmd` (structure test)
- ✅ `TestNew<Resource>CmdRunE` (execution test)
- ✅ `TestNew<Resource>Cmd_AutocompleteRegistration` (autocomplete)

## Tools and Commands

### Useful grep patterns:

```bash
# Find all command constructors
grep -r "^func New.*Cmd(" cmd/kuke --include="*.go" | grep -v "_test.go"

# Find all test functions
grep -r "^func Test" cmd/kuke --include="*_test.go"

# Find specific test types
grep -r "^func Test.*RunE" cmd/kuke --include="*_test.go"
grep -r "^func Test.*AutocompleteRegistration" cmd/kuke --include="*_test.go"
grep -r "^func Test.*RegistersSubcommands" cmd/kuke --include="*_test.go"
```

### Useful find patterns:

```bash
# Find all test files
find cmd/kuke -name "*_test.go" -type f

# Find commands without test files
# (requires manual verification)
```

## Verification Checklist

When performing test consistency verification, ensure:

- [ ] All command files have corresponding test files
- [ ] All parent commands have metadata, subcommand registration, and autocomplete tests
- [ ] All resource commands have execution and autocomplete tests
- [ ] All create commands have output formatting tests (`TestPrint<Resource>Result`)
- [ ] All get commands have output formatting tests (`TestPrint<Resource>`)
- [ ] Commands with persistent flags have flag and viper binding tests
- [ ] Test function naming follows consistent patterns
- [ ] Special commands (init, version, autocomplete) are documented separately
- [ ] Report includes prioritized recommendations
- [ ] Report includes comparison matrices for easy reference

## Frequency

This verification process should be performed:
- **After major refactoring** of command structure
- **Before releases** to ensure test quality
- **When adding new command types** to ensure consistency
- **Periodically** (e.g., quarterly) to catch regressions

## Output Format

The final report should be saved as `TESTING_CONSISTENCY_REPORT.md` in the project root and include:

1. **Executive Summary** - High-level overview
2. **Detailed Analysis** - Command-by-command breakdown
3. **Summary Matrices** - Visual comparison of test coverage
4. **Recommendations** - Prioritized list of improvements
5. **Consistency Patterns** - Documented patterns and inconsistencies
6. **Conclusion** - Overall assessment

## Questions?

If you're unsure about the process, refer to:

1. `TESTING_CONSISTENCY_REPORT.md` - Example of completed analysis
2. `cmd/kuke/delete/cell/` - Reference implementation with complete tests
3. `cmd/kuke/create/space/` - Reference implementation with complete tests
4. `cmd/kuke/get/stack/` - Reference implementation with complete tests

---

# API Scheme and Model Hub Validation Guidelines

This document establishes the **mandatory pattern** for maintaining decoupling between `internal/` packages and `pkg/api` through the `apischeme` and `modelhub` packages. This pattern enables support for multiple API versions without coupling internal logic to external API structures.

## Core Principle: Decoupling Through Conversion Layer

All `internal/` packages (except `internal/apischeme`) MUST NOT directly import `pkg/api/model/*` packages. Instead, they should:

1. Work with internal modelhub types for business logic
2. Use apischeme conversions at boundaries (input/output)
3. Let the conversion layer handle version translation

## Architecture Pattern

### 1. ModelHub Types (Internal Representation)

ModelHub types in `internal/modelhub/` provide version-agnostic internal representations:

```go
// internal/modelhub/realm.go
type Realm struct {
    Metadata RealmMetadata
    Spec     RealmSpec
    Status   RealmStatus
}

type RealmMetadata struct {
    Name   string
    Labels map[string]string
}

type RealmSpec struct {
    Namespace string
}

type RealmStatus struct {
    State RealmState
}
```

**Characteristics:**
- No version information
- Stable structure across API versions
- Used by all internal business logic

### 2. Apischeme Conversions (Version Translation Layer)

Apischeme functions in `internal/apischeme/` convert between external API types and internal modelhub types:

```go
// internal/apischeme/scheme.go
func ConvertRealmDocToInternal(in ext.RealmDoc) (intmodel.Realm, error)
func BuildRealmExternalFromInternal(in intmodel.Realm, apiVersion ext.Version) (ext.RealmDoc, error)
func NormalizeRealm(req ext.RealmDoc) (intmodel.Realm, ext.Version, error)
```

**Pattern for Each Resource:**
- `Convert*DocToInternal` - External → Internal (handles version switching)
- `Build*ExternalFromInternal` - Internal → External (for specified version)
- `Normalize*` - External → Internal with version defaulting

### 3. Conversion Boundary Pattern

Conversions should happen at boundaries:

```go
// Input boundary: Convert external to internal
internal, version, err := apischeme.NormalizeRealm(*externalDoc)

// Business logic: Work with internal types (version-agnostic)
internal.Status.State = intmodel.RealmStateReady

// Output boundary: Convert internal back to external
external, err := apischeme.BuildRealmExternalFromInternal(internal, version)
```

## Implementation Requirements

### 1. All Resources MUST Have ModelHub Types

**Required Files:**
- `internal/modelhub/realm.go` ✅
- `internal/modelhub/space.go` ✅
- `internal/modelhub/stack.go` ❌ (missing)
- `internal/modelhub/cell.go` ❌ (missing)
- `internal/modelhub/container.go` ❌ (missing)

**Pattern:**
```go
type <Resource> struct {
    Metadata <Resource>Metadata
    Spec     <Resource>Spec
    Status   <Resource>Status
}
```

### 2. All Resources MUST Have Apischeme Conversions

**Required Functions (per resource):**
- `Convert<Resource>DocToInternal(ext.<Resource>Doc) -> (intmodel.<Resource>, error)`
- `Build<Resource>ExternalFromInternal(intmodel.<Resource>, ext.Version) -> (ext.<Resource>Doc, error)`
- `Normalize<Resource>(ext.<Resource>Doc) -> (intmodel.<Resource>, ext.Version, error)`

**Version Handling:**
- Switch statements to handle different API versions
- Default empty version to latest supported version
- Return error for unsupported versions

### 3. Controllers MUST Use Internal Types

**Controller Methods Should:**
- Accept internal types as parameters (or convert at entry)
- Return internal types (or convert at exit)
- Work with modelhub types throughout business logic

**Example:**
```go
// GOOD: Controller works with internal types
func (r *Exec) CreateRealm(internal intmodel.Realm) (*v1beta1.RealmDoc, error) {
    // Work with internal type
    internal.Status.State = intmodel.RealmStateCreating
    
    // Convert at boundary
    version := apischeme.VersionV1Beta1
    external, err := apischeme.BuildRealmExternalFromInternal(internal, version)
    return &external, err
}

// BAD: Controller directly uses external types
func (r *Exec) CreateRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
    // Direct manipulation of external API type - tightly coupled!
    doc.Status.State = v1beta1.RealmStateCreating
    return doc, nil
}
```

### 4. Utility Functions MUST Use Internal Types

**Utility Functions Should:**
- Accept internal types as parameters
- Work with modelhub types
- Not import `pkg/api` packages

**Example:**
```go
// GOOD: Utility uses internal types
func BuildSpaceNetworkName(internal intmodel.Space) (string, error) {
    // Work with internal type
    return fmt.Sprintf("%s-%s", internal.Spec.RealmName, internal.Metadata.Name), nil
}

// BAD: Utility uses external types
func BuildSpaceNetworkName(doc *v1beta1.SpaceDoc) (string, error) {
    // Direct access to external API - tightly coupled!
    return fmt.Sprintf("%s-%s", doc.Spec.RealmID, doc.Metadata.Name), nil
}
```

## Import Rules

### ✅ ALLOWED Imports

**Only `internal/apischeme` can import `pkg/api`:**
- `internal/apischeme/scheme.go` ✅ (conversion layer)
- `internal/apischeme/scheme_test.go` ✅ (test file)

**All other `internal/` packages MUST NOT import `pkg/api/model/*`**

### ❌ PROHIBITED Imports

**These patterns are violations:**
```go
// BAD: Direct import in controller
import v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"

// BAD: Direct import in utility
import v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"

// BAD: Direct import in any internal package except apischeme
```

### ⚠️ SPECIAL CASES

**Daemon API (`pkg/api/apidaemon`):**
- `internal/daemon` importing `pkg/api/apidaemon` is currently acceptable
- This is a control API, not a resource API
- Less likely to require versioning
- Consider abstracting if versioning becomes necessary

## Test Requirements

### 1. Round-Trip Tests

**All resources MUST have round-trip conversion tests:**

```go
func Test<Resource>RoundTripV1Beta1(t *testing.T) {
    // External → Internal
    internal, version, err := apischeme.Normalize<Resource>(externalInput)
    
    // Modify internal (simulate controller logic)
    internal.Status.State = intmodel.<Resource>StateReady
    
    // Internal → External
    external, err := apischeme.Build<Resource>ExternalFromInternal(internal, version)
    
    // Validate round-trip
    // ...
}
```

### 2. Version Support Tests

**Test all supported versions:**
- Test conversion for each API version
- Test error handling for unsupported versions
- Test version defaulting (empty version)

### 3. Field Mapping Tests

**Test field name differences:**
- External uses `RealmID`, internal uses `RealmName`
- Test that conversions handle field name mappings correctly
- Test that all fields are preserved through conversion

## Validation Process

This section documents the **mandatory process** for validating apischeme and modelhub coupling. This process should be performed periodically to ensure decoupling is maintained.

## Step-by-Step Validation Process

### Step 1: Catalog Resource Coverage

**Objective:** Identify all resource types and their modelhub/apischeme coverage.

**Actions:**
1. List all `*Doc` types in `pkg/api/model/v1beta1/`:
   ```bash
   grep -r "^type.*Doc struct" pkg/api/model/v1beta1
   ```

2. Check for corresponding modelhub types:
   ```bash
   find internal/modelhub -name "*.go" -type f
   ```

3. Check for apischeme conversion functions:
   ```bash
   grep -r "^func (Convert|Build|Normalize)" internal/apischeme
   ```

**Expected Output:**
- Complete list of resource types in pkg/api
- Coverage matrix showing which resources have modelhub types
- Coverage matrix showing which resources have apischeme conversions

### Step 2: Analyze Import Violations

**Objective:** Identify all direct imports from `internal/` to `pkg/api`.

**Actions:**
1. Search for all `pkg/api` imports in `internal/`:
   ```bash
   grep -r "pkg/api" internal/ --include="*.go"
   ```

2. Categorize by package:
   - Controller packages
   - Utility packages
   - Apischeme package (allowed)
   - Other packages

3. Identify purpose of each import:
   - Type usage in function signatures
   - Direct type construction
   - Field access

**Expected Output:**
- Complete list of import violations
- Categorized by package and purpose
- Identification of acceptable vs. problematic imports

### Step 3: Validate Conversion Patterns

**Objective:** Verify conversion functions are complete and correct.

**Actions:**
1. Check for all three conversion functions per resource:
   - `Convert*DocToInternal`
   - `Build*ExternalFromInternal`
   - `Normalize*`

2. Review conversion correctness:
   - Field mappings are correct
   - State type conversions are correct
   - Error handling for unsupported versions

3. Verify test coverage:
   - Round-trip tests exist
   - Version handling is tested
   - Error cases are covered

**Expected Output:**
- List of complete conversion patterns
- List of missing conversion functions
- Test coverage assessment

### Step 4: Analyze Controller Usage

**Objective:** Determine how controllers use conversions vs. direct API types.

**Actions:**
1. Search for apischeme usage in controllers:
   ```bash
   grep -r "apischeme\\.(Normalize|Convert|Build)" internal/controller
   ```

2. Search for direct pkg/api usage:
   ```bash
   grep -r "v1beta1\\." internal/controller
   ```

3. Identify conversion boundaries:
   - Where conversions happen
   - Where they should happen but don't
   - Where controllers bypass conversions

**Expected Output:**
- List of controllers using apischeme correctly
- List of controllers bypassing conversions
- Identification of conversion boundaries

### Step 5: Check Version Support

**Objective:** Assess readiness for multiple API versions.

**Actions:**
1. Check supported versions:
   ```bash
   grep -r "VersionV1Beta1\\|VersionV1\\|VersionV1Beta2" internal/apischeme
   ```

2. Review version handling:
   - How versions are selected
   - Default version logic
   - Error handling for unsupported versions

3. Assess multi-version readiness:
   - Conversion layer supports version switching
   - Controllers work with internal types
   - Storage layer is version-aware

**Expected Output:**
- Current version support status
- Multi-version readiness assessment
- Gaps in version handling

### Step 6: Generate Validation Report

**Objective:** Create comprehensive report documenting findings.

**Actions:**
1. **Create Summary Section:**
   - Overall decoupling status
   - Coverage statistics
   - Key findings

2. **Create Coverage Matrices:**
   - Resource coverage (ModelHub vs. Apischeme)
   - Controller operation coverage
   - Import violation summary

3. **Create Recommendations Section:**
   - High priority: Missing modelhub types and conversions
   - Medium priority: Controller refactoring
   - Low priority: Enhancement opportunities

4. **Create Consistency Patterns:**
   - Document reference implementation (Realm/Space)
   - Document gaps and inconsistencies
   - Provide examples for missing resources

**Expected Output:**
- Comprehensive markdown report (`API_SCHEME_VALIDATION_REPORT.md`)
- Prioritized recommendations
- Actionable items for achieving full decoupling

## Validation Checklist

When performing validation, ensure:

- [ ] All resource types have corresponding modelhub types
- [ ] All resource types have complete apischeme conversions (3 functions each)
- [ ] All resources have round-trip conversion tests
- [ ] Controllers use internal types (not external API types)
- [ ] Utility functions use internal types (not external API types)
- [ ] No direct `pkg/api/model/*` imports except in `internal/apischeme`
- [ ] Conversion happens at boundaries (input/output)
- [ ] Version handling is correct (defaulting, error handling)
- [ ] Report includes coverage matrices
- [ ] Report includes prioritized recommendations

## Frequency

This validation process should be performed:

- **After adding new resource types** - Ensure they follow the pattern
- **When adding new API versions** - Verify conversion layer supports them
- **Before major refactorings** - Baseline current state
- **Periodically** (e.g., quarterly) - Catch regressions and violations

## Output Format

The validation report should be saved as `API_SCHEME_VALIDATION_REPORT.md` in the project root and include:

1. **Executive Summary** - High-level decoupling status
2. **Resource Coverage** - Matrix showing modelhub/apischeme coverage
3. **Import Analysis** - Violations and acceptable imports
4. **Conversion Validation** - Pattern completeness and correctness
5. **Controller Usage** - How controllers use conversions
6. **Version Support** - Multi-version readiness
7. **Recommendations** - Prioritized action items
8. **Consistency Patterns** - Reference implementations and gaps

## Examples from Codebase

### Reference Implementation: Realm/Space

**ModelHub Types:**
- `internal/modelhub/realm.go` - Complete Realm type
- `internal/modelhub/space.go` - Complete Space type

**Apischeme Conversions:**
- `internal/apischeme/scheme.go` - Complete conversion functions

**Usage Pattern:**
- `internal/controller/runner/runner.go:134` - `CreateRealm` uses apischeme
- `internal/controller/runner/runner.go:419` - `CreateSpace` uses apischeme

**Test Coverage:**
- `internal/apischeme/scheme_test.go` - Round-trip test for Realm

### Missing Implementation: Stack, Cell, Container

**Required Actions:**
1. Create modelhub types (`internal/modelhub/stack.go`, etc.)
2. Implement apischeme conversions
3. Add round-trip tests
4. Refactor controllers to use internal types

## Questions?

If you're unsure about the pattern, refer to:

1. `API_SCHEME_VALIDATION_REPORT.md` - Example of completed validation
2. `internal/modelhub/realm.go` - Reference modelhub type
3. `internal/apischeme/scheme.go` - Reference conversion functions
4. `internal/controller/runner/runner.go:134` - Reference usage pattern

All resources should follow the Realm/Space pattern for consistency.
