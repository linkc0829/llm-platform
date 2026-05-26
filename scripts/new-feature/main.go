// Command new-feature scaffolds a new feature package by copying the `order`
// feature as a template and rewriting identifiers.
//
// Usage:
//
//	go run ./scripts/new-feature -name=inventory
//
// or via Make:
//
//	make new-feature name=inventory
//
// After scaffolding you still need to:
//   - Edit internal/bootstrap/wire.go to register the feature
//   - Edit api/openapi.yaml to declare the endpoints
//   - Edit sql/queries/<name>.sql (a stub is created for you)
//   - Create migrations: make migrate-create name=create_<name>
//   - Run: make sqlc-generate && make mock-gen && make lint && make test
//
// Note on plurals: identifiers like `Orders` are rewritten with a naive `+s`,
// so an `inventory` feature ends up with `Inventorys`. Fix to `Inventories`
// by hand — automatic English pluralization is a rabbit hole this script
// declines to enter.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const templateFeature = "order"

func main() {
	var name string
	flag.StringVar(&name, "name", "", "snake_case feature name (e.g. inventory)")
	flag.Parse()

	if name == "" {
		log.Fatal("usage: new-feature -name=<snake_case_name>")
	}
	if !isSnakeCase(name) {
		log.Fatalf("feature name must be lower snake_case (got %q)", name)
	}
	if name == templateFeature {
		log.Fatalf("cannot name a feature %q (that's the template source)", templateFeature)
	}

	root, err := repoRoot()
	if err != nil {
		log.Fatalf("locate repo root: %v", err)
	}

	src := filepath.Join(root, "internal", templateFeature)
	dst := filepath.Join(root, "internal", name)

	if _, err := os.Stat(dst); err == nil {
		log.Fatalf("destination already exists: %s", dst)
	}

	transform := identTransform(templateFeature, name)

	if err := copyTree(src, dst, transform); err != nil {
		log.Fatalf("copy feature: %v", err)
	}

	// Create a sqlc query stub.
	sqlStub := filepath.Join(root, "sql", "queries", name+".sql")
	if _, err := os.Stat(sqlStub); os.IsNotExist(err) {
		if err := os.WriteFile(sqlStub, []byte(sqlStubContent(name)), 0o644); err != nil {
			log.Fatalf("create sql stub: %v", err)
		}
	}

	fmt.Printf("scaffolded internal/%s/ from internal/%s/\n", name, templateFeature)
	fmt.Printf("scaffolded sql/queries/%s.sql\n\n", name)
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit internal/bootstrap/wire.go — construct repo/service/handler and call RegisterRoutes")
	fmt.Println("  2. Add per-feature depguard rule in .golangci.yml (see the order/payment/user blocks)")
	fmt.Println("  3. Edit api/openapi.yaml — declare endpoints under a new tag")
	fmt.Printf("  4. make migrate-create name=create_%s — create migration files\n", name)
	fmt.Println("  5. make sqlc-generate && make mock-gen")
	fmt.Println("  6. make lint && make test")
	fmt.Println()
	fmt.Println("Then gut the bodies — the copied code is order-shaped, not yours.")
	fmt.Println("Each .go file is prefixed with a TODO banner as a reminder.")
}

// ---------------------------------------------------------------------------

func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case unicode.IsLower(r):
		case unicode.IsDigit(r):
			if i == 0 {
				return false
			}
		case r == '_':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// identTransform produces a function that rewrites identifiers from the
// template feature to the new feature. Plain string replacement, longest
// first — this correctly handles CamelCase compounds like `CreateOrderRequest`,
// `OrderID`, `toOrderResponse` because `Order` appears inside the identifier
// as a literal substring.
//
// False positives are unlikely in this codebase: there is no `Reorder`,
// `Ordering`, `Border`, etc. in the template package. If a future template
// introduces such a word, swap this for an AST-based renamer.
func identTransform(from, to string) func(string) string {
	fromTitle := title(from) // Order
	toTitle := title(to)     // Inventory
	fromUpper := strings.ToUpper(from)
	toUpper := strings.ToUpper(to)

	// Order matters: longer + uppercase forms first to avoid double-replacing.
	pairs := [][2]string{
		{fromUpper + "_", toUpper + "_"}, // ORDER_FOO constants
		{fromTitle, toTitle},             // Order, OrderID, CreateOrderRequest, ...
		{from, to},                       // package name, var names, sql identifiers
	}

	return func(s string) string {
		for _, p := range pairs {
			s = strings.ReplaceAll(s, p[0], p[1])
		}
		return s
	}
}

func title(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

const todoBanner = `// TODO(%s): this file was scaffolded from internal/order/ — gut the bodies,
// drop unused fields, and replace the order-specific logic with yours.

`

func copyTree(src, dst string, transform func(string) string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		// Skip generated mocks — they'll be regenerated.
		sep := string(filepath.Separator)
		if strings.Contains(rel, sep+"mocks"+sep) || strings.HasPrefix(rel, "mocks"+sep) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		if strings.HasSuffix(rel, ".go") {
			body := transform(string(data))
			// Insert TODO banner after the package clause.
			body = injectBannerAfterPackage(body, transform)
			data = []byte(body)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// injectBannerAfterPackage finds the first `package <x>` line and inserts a
// TODO banner immediately after it, so go-vet / IDE don't see an out-of-place
// comment before the package clause.
func injectBannerAfterPackage(src string, transform func(string) string) string {
	const marker = "\npackage "
	idx := strings.Index(src, marker)
	if idx < 0 {
		// File doesn't start with whitespace + package — try start of file.
		if strings.HasPrefix(src, "package ") {
			idx = -1
		} else {
			return src // unrecognized file; leave alone
		}
	}
	// Find end of the package line.
	lineEnd := strings.IndexByte(src[idx+1:], '\n')
	if lineEnd < 0 {
		return src
	}
	insertAt := idx + 1 + lineEnd + 1
	// Use the transformed feature name as the TODO marker so it shows up
	// distinctly per package.
	banner := fmt.Sprintf(todoBanner, transform(templateFeature))
	return src[:insertAt] + "\n" + banner + src[insertAt:]
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", wd)
		}
		dir = parent
	}
}

func sqlStubContent(name string) string {
	return fmt.Sprintf(`-- Queries for the %s feature. Regenerate with: make sqlc-generate
-- Convention: use sqlc.arg(<name>), not positional $N.

-- name: Get%sItem :one
SELECT * FROM %s_items WHERE id = sqlc.arg(id);

-- name: Create%sItem :exec
INSERT INTO %s_items (id) VALUES (sqlc.arg(id));
`, name, title(name), name, title(name), name)
}
