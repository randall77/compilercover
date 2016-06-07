package main

// This is a program which annotates the Go compiler to provide
// coverage reports on the compiler itself. Use it like this:
//    rm /tmp/cover.out                  # remove old data, if any
//    checkout a go repository           # clean, will be heavily modified
//    set up GOROOT/PATH
//    cd go/src
//    compilercover                      # add coverage
//    ./all.bash                         # run all tests with coverage enabled
//    git checkout .                     # delete coverage (1)
//    rm cmd/compile/cover.go            # delete coverage (2)
//    ./make.bash                        # recompile without coverage
//    go tool cover -html=/tmp/cover.out # display results
//
// Experimental! Run this in a client that is otherwise clean and has
// nothing critical in it.
import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	// Add coverage instrumentation to all files in cmd/compile.
	err = filepath.Walk(filepath.Join(root, "cmd", "compile"), visit)
	if err != nil {
		panic(err)
	}

	// Add cover.go driver.
	genCover()
}

var n int

var pkgs = map[string]bool{}
var importText string
var initText string

func visit(path string, f os.FileInfo, err error) error {
	i := strings.Index(path, "cmd/compile/")
	if i == -1 {
		return nil
	}
	path = path[i:]
	dir, name := filepath.Split(path)
	dir = dir[:len(dir)-1]
	_, pkg := filepath.Split(dir)

	if !strings.HasSuffix(name, ".go") {
		return nil
	}
	if strings.HasSuffix(name, "_test.go") {
		return nil
	}
	if name == "mkbuiltin.go" {
		// TODO: read build tags instead?
		return nil
	}
	if name == "builtin.go" {
		// a test checks timestamps of mkbuiltin.go vs builtin.go
		return nil
	}
	if pkg == "gen" || // don't do generator files.
		pkg == "testdata" || // don't do test files.
		pkg == "builtin" ||
		pkg == "test" ||
		pkg == "compile" { // circular import dependence
		return nil
	}
	if !pkgs[dir] {
		pkgs[dir] = true
		importText += fmt.Sprintf("\t\"%s\"\n", dir)
	}

	// run "go tool cover -mode=set -var GoCover_%d file"
	fmt.Printf("processing %s\n", path)
	c := exec.Command("go", "tool", "cover", "-mode=set", "-var",
		fmt.Sprintf("GoCover_%d", n), path)
	out, err := c.Output()
	if err != nil {
		os.Stdout.Write(out)
		panic(err)
	}
	// write output back to file
	ioutil.WriteFile(path, out, 0666)

	initText += fmt.Sprintf("\tcoverRegisterFile(\"%s\", %s.GoCover_%d.Count[:], %s.GoCover_%d.Pos[:], %s.GoCover_%d.NumStmt[:])\n", path, pkg, n, pkg, n, pkg, n)

	n++
	return nil
}

func genCover() {
	fmt.Println("generating cover.go")
	// write out cover code.
	text := fmt.Sprintf(`
package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
%s
)

func init() {
%s
	cover = testing.Cover{
                Mode:            "set",
                Counters:        coverCounters,
                Blocks:          coverBlocks,
                CoveredPackages: " in ./...",
        }
	gc.AtExit(coverageReport)
}

var (
	cover         testing.Cover
	coverCounters = make(map[string][]uint32)
	coverBlocks   = make(map[string][]testing.CoverBlock)
)

func coverRegisterFile(fileName string, counter []uint32, pos []uint32, numStmts []uint16) {
	if 3*len(counter) != len(pos) || len(counter) != len(numStmts) {
		panic("coverage: mismatched sizes")
	}
	if coverCounters[fileName] != nil {
		// Already registered.
		return
	}
	coverCounters[fileName] = counter
	block := make([]testing.CoverBlock, len(counter))
	for i := range counter {
		block[i] = testing.CoverBlock{
			Line0: pos[3*i+0],
			Col0:  uint16(pos[3*i+2]),
			Line1: pos[3*i+1],
			Col1:  uint16(pos[3*i+2] >> 16),
			Stmts: numStmts[i],
		}
	}
	coverBlocks[fileName] = block
}

func coverageReport() {
	f, err := os.OpenFile("/tmp/cover.out", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
	mustBeNil(err)
	s, err := f.Stat()
	mustBeNil(err)
	if s.Size() == 0 {
		fmt.Fprintf(f, "mode: %%s\n", cover.Mode)
	}
	defer func() { mustBeNil(f.Close()) }()

	for name, counts := range cover.Counters {
		blocks := cover.Blocks[name]
		for i := range counts {
			stmts := int64(blocks[i].Stmts)
			count := atomic.LoadUint32(&counts[i]) // For -mode=atomic.
			_, err := fmt.Fprintf(f, "%%s:%%d.%%d,%%d.%%d %%d %%d\n", name,
				blocks[i].Line0, blocks[i].Col0,
				blocks[i].Line1, blocks[i].Col1,
				stmts,
				count)
			mustBeNil(err)
		}
	}
}

func mustBeNil(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "cover: %%s\n", err)
		os.Exit(2)
	}
}
`, importText, initText)
	err := ioutil.WriteFile("cmd/compile/cover.go", []byte(text), 0666)
	if err != nil {
		panic(err)
	}
}
