// Command jsx2scss generates an SCSS skeleton from the BEM class names used
// in a JSX/TSX file.
//
// Usage:
//
//	jsx2scss [flags] [file]
//
// It reads the file given by -i (or as the only argument), or standard input
// when there is none or it is "-". It writes SCSS to standard output, or to
// the file given by -o. Problems are reported on standard error.
//
// Exit status: 0 on success, 1 if the input has errors or I/O failed, 2 for
// bad usage.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/generator"
)

const usage = `Usage: jsx2scss [flags] [file]

Generate an SCSS skeleton from the BEM class names in a JSX/TSX file.
Reads file, or standard input if file is missing or "-".

Flags:
  -i, -input FILE     input file (default: standard input)
  -o, -output FILE    output file (default: standard output)
  -indent N|tab       indentation: number of spaces, or "tab" (default: 2)
  -nesting dom|bem    dom: nest by JSX tree (default)
                      bem: nest by class name, one rule per block
  -naming PRESET      class naming convention (default: classic)
                      classic  block__elem--mod
                      kv       block__elem--key_value
                      react    Block-elem_mod_value
  -force              write output even if the input has errors
  -h, -help           show this help
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// errUsage marks errors caused by bad command-line usage.
var errUsage = errors.New("usage")

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	err := runErr(args, stdin, stdout, stderr)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "jsx2scss: %v\n\n%s", err, usage)
		return 2
	default:
		fmt.Fprintf(stderr, "jsx2scss: %v\n", err)
		return 1
	}
}

func runErr(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("jsx2scss", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var in, out, indent, nesting, naming string
	var force bool
	fs.StringVar(&in, "i", "", "")
	fs.StringVar(&in, "input", "", "")
	fs.StringVar(&out, "o", "", "")
	fs.StringVar(&out, "output", "", "")
	fs.StringVar(&indent, "indent", "2", "")
	fs.StringVar(&nesting, "nesting", "dom", "")
	fs.StringVar(&naming, "naming", bem.PresetClassic, "")
	fs.BoolVar(&force, "force", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}

	switch fs.NArg() {
	case 0:
	case 1:
		if in != "" {
			return fmt.Errorf("%w: input given both by -i and as an argument", errUsage)
		}
		in = fs.Arg(0)
	default:
		return fmt.Errorf("%w: too many arguments", errUsage)
	}

	opts := generator.Options{}
	var err error
	if opts.Indent, err = parseIndent(indent); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if opts.Nesting, err = generator.ParseNesting(nesting); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if naming == bem.PresetCustom {
		return fmt.Errorf("%w: the custom naming preset needs a config file, which the CLI does not read yet", errUsage)
	}
	if opts.Naming, err = bem.NewNaming(bem.NamingConfig{Preset: naming}); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}

	name, src, err := readInput(in, stdin)
	if err != nil {
		return err
	}

	root, parseErr := core.ParseString(name, string(src))
	var list core.ErrorList
	if errors.As(parseErr, &list) {
		for _, e := range list {
			fmt.Fprintf(stderr, "%s:%v\n", name, e)
		}
		if !force {
			return fmt.Errorf("%s: %d error(s); use -force to write output anyway", name, len(list))
		}
	}

	result := generator.Generate(root, opts)
	if out == "" || out == "-" {
		_, err = io.WriteString(stdout, result)
		return err
	}
	return os.WriteFile(out, []byte(result), 0o644)
}

func readInput(path string, stdin io.Reader) (name string, src []byte, err error) {
	if path == "" || path == "-" {
		if f, ok := stdin.(*os.File); ok && path == "" && isTerminal(f) {
			return "", nil, fmt.Errorf("%w: no input file given and standard input is a terminal", errUsage)
		}
		src, err = io.ReadAll(stdin)
		return "<stdin>", src, err
	}
	src, err = os.ReadFile(path)
	return path, src, err
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func parseIndent(s string) (string, error) {
	if strings.EqualFold(s, "tab") {
		return "\t", nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 16 {
		return "", fmt.Errorf("invalid -indent %q (want 1-16 or tab)", s)
	}
	return strings.Repeat(" ", n), nil
}
