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
// Settings (naming convention, indentation, nesting, ignore patterns) are
// read from bem.config.json: the file given by -config, or the first one
// found from the input file's directory upwards, up to the project root
// (a directory containing .git). Command-line flags override it.
//
// Exit status: 0 on success, 1 if the input or configuration has errors or
// I/O failed, 2 for bad usage.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/config"
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
                      custom   separators and patterns from the config
  -config FILE        read settings from FILE (default: nearest bem.config.json)
  -no-config          don't look for bem.config.json
  -force              write output even if the input has errors
  -version            print the version
  -h, -help           show this help
`

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

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
	var in, out, indent, nesting, naming, configPath string
	var force, noConfig, showVersion bool
	fs.StringVar(&in, "i", "", "")
	fs.StringVar(&in, "input", "", "")
	fs.StringVar(&out, "o", "", "")
	fs.StringVar(&out, "output", "", "")
	fs.StringVar(&indent, "indent", "2", "")
	fs.StringVar(&nesting, "nesting", "dom", "")
	fs.StringVar(&naming, "naming", bem.PresetClassic, "")
	fs.StringVar(&configPath, "config", "", "")
	fs.BoolVar(&noConfig, "no-config", false, "")
	fs.BoolVar(&force, "force", false, "")
	fs.BoolVar(&showVersion, "version", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}

	if showVersion {
		_, err := fmt.Fprintln(stdout, "jsx2scss", version)
		return err
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

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if configPath != "" && noConfig {
		return fmt.Errorf("%w: -config and -no-config are mutually exclusive", errUsage)
	}

	settings, err := loadSettings(configPath, noConfig, in)
	if err != nil {
		return err
	}
	if set["naming"] {
		settings.Naming.Preset = naming
	}
	namingConv, err := settings.NamingConvention()
	if err != nil {
		if set["naming"] {
			return fmt.Errorf("%w: %v", errUsage, err)
		}
		return fmt.Errorf("config: %v", err)
	}
	opts, err := settings.GeneratorOptions(namingConv)
	if err != nil {
		return fmt.Errorf("config: %v", err)
	}
	if set["indent"] {
		if opts.Indent, err = parseIndent(indent); err != nil {
			return fmt.Errorf("%w: %v", errUsage, err)
		}
	}
	if set["nesting"] {
		if opts.Nesting, err = generator.ParseNesting(nesting); err != nil {
			return fmt.Errorf("%w: %v", errUsage, err)
		}
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

// loadSettings reads the configuration file: explicit, found near the
// input, or none.
func loadSettings(explicit string, disabled bool, input string) (config.Settings, error) {
	path := explicit
	if path == "" && !disabled {
		dir := "."
		if input != "" && input != "-" {
			dir = filepath.Dir(input)
		}
		path = config.Find(dir)
	}
	if path == "" {
		return config.Settings{}, nil
	}
	raw, err := config.LoadFile(path)
	if err != nil {
		return config.Settings{}, err
	}
	return config.Parse(raw, true)
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
