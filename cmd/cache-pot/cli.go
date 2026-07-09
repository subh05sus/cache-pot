// The "cli" subcommand is an interactive RESP client — a redis-cli-style shell
// for talking to a running Cache-Pot server. It reads commands line by line,
// sends them over the wire, and prints replies the way redis-cli does. With a
// piped stdin it runs non-interactively, so it doubles as a scripting tool.
package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/subh05sus/cache-pot/internal/client"
	"github.com/subh05sus/cache-pot/internal/resp"
)

func runCLI(argv []string) error {
	fs := flag.NewFlagSet("cache-pot cli", flag.ExitOnError)
	addr := fs.String("addr", env("CACHEPOT_ADDR", "localhost:6379"), "server address (host:port)")
	password := fs.String("auth", env("CACHEPOT_AUTH", ""), "AUTH password sent on connect")
	useTLS := fs.Bool("tls", false, "connect over TLS")
	caCert := fs.String("tls-cacert", "", "CA certificate to verify the server (PEM)")
	insecure := fs.Bool("tls-insecure", false, "skip TLS certificate verification")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	fs.Parse(argv)

	// A one-shot command can be passed as trailing args: `cache-pot cli PING`.
	oneShot := fs.Args()

	c, err := dial(*addr, *useTLS, *caCert, *insecure)
	if err != nil {
		return err
	}
	defer c.Close()

	if *password != "" {
		if _, err := c.Do("AUTH", *password); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	color := !*noColor && isTerminal(os.Stdout)
	p := &printer{color: color, out: os.Stdout}

	if len(oneShot) > 0 {
		v, err := c.DoRaw(oneShot...)
		if err != nil {
			return err
		}
		p.print(v)
		return nil
	}

	interactive := isTerminal(os.Stdin)
	return repl(c, p, *addr, interactive)
}

func dial(addr string, useTLS bool, caCert string, insecure bool) (*client.Client, error) {
	if !useTLS {
		return client.Dial(addr)
	}
	cfg := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // opt-in via --tls-insecure
	if caCert != "" {
		pem, err := os.ReadFile(caCert)
		if err != nil {
			return nil, fmt.Errorf("read ca cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", caCert)
		}
		cfg.RootCAs = pool
	}
	return client.DialTLS(addr, cfg)
}

// repl runs the read-eval-print loop until EOF or an explicit exit.
func repl(c *client.Client, p *printer, addr string, interactive bool) error {
	hist := openHistory(interactive)
	if hist != nil {
		defer hist.Close()
	}

	if interactive {
		fmt.Printf("Cache-Pot CLI — connected to %s\n", addr)
		fmt.Println(`Type a command, "help" for tips, or "exit" to quit.`)
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for {
		if interactive {
			fmt.Printf("%s> ", addr)
		}
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		switch strings.ToLower(line) {
		case "exit", "quit":
			return nil
		case "clear":
			if interactive {
				fmt.Print("\033[2J\033[H")
			}
			continue
		case "help":
			printHelp(p.color)
			continue
		}

		args, err := tokenize(line)
		if err != nil {
			p.printErr(err.Error())
			continue
		}
		if len(args) == 0 {
			continue
		}
		if hist != nil {
			fmt.Fprintln(hist, line)
		}

		v, err := c.DoRaw(args...)
		if err != nil {
			// A transport error means the connection is gone; stop the loop.
			return fmt.Errorf("connection lost: %w", err)
		}
		p.print(v)
	}
	return sc.Err()
}

// tokenize splits a command line into arguments, honoring single quotes
// (literal) and double quotes (with \n \r \t \\ \" and \xHH escapes), the same
// rules redis-cli uses.
func tokenize(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	i := 0
	for i < len(line) {
		ch := line[i]
		switch {
		case ch == ' ' || ch == '\t':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
			i++
		case ch == '\'':
			inArg = true
			i++
			for i < len(line) && line[i] != '\'' {
				cur.WriteByte(line[i])
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unbalanced single quotes")
			}
			i++ // closing quote
		case ch == '"':
			inArg = true
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
					switch line[i] {
					case 'n':
						cur.WriteByte('\n')
					case 'r':
						cur.WriteByte('\r')
					case 't':
						cur.WriteByte('\t')
					case 'x':
						if i+2 < len(line) {
							if b, err := strconv.ParseUint(line[i+1:i+3], 16, 8); err == nil {
								cur.WriteByte(byte(b))
								i += 3
								continue
							}
						}
						cur.WriteByte(line[i])
					default:
						cur.WriteByte(line[i])
					}
					i++
					continue
				}
				cur.WriteByte(line[i])
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unbalanced double quotes")
			}
			i++ // closing quote
		default:
			inArg = true
			cur.WriteByte(ch)
			i++
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

// printer renders RESP replies. When color is on it uses ANSI codes matched to
// each RESP type.
type printer struct {
	color bool
	out   io.Writer
}

const (
	cReset = "\033[0m"
	cGreen = "\033[32m" // simple strings
	cRed   = "\033[31m" // errors
	cCyan  = "\033[36m" // integers
	cGray  = "\033[90m" // nil, index markers
)

func (p *printer) tint(code, s string) string {
	if !p.color {
		return s
	}
	return code + s + cReset
}

func (p *printer) print(v resp.Value) { p.printIndent(v, "") }

func (p *printer) printErr(msg string) {
	fmt.Fprintln(p.out, p.tint(cRed, "(error) "+msg))
}

func (p *printer) printIndent(v resp.Value, indent string) {
	switch v.Kind {
	case '+':
		fmt.Fprintln(p.out, p.tint(cGreen, v.Str))
	case '-':
		fmt.Fprintln(p.out, p.tint(cRed, "(error) "+v.Str))
	case ':':
		fmt.Fprintln(p.out, p.tint(cCyan, "(integer) "+strconv.FormatInt(v.Int, 10)))
	case '$':
		if v.Null {
			fmt.Fprintln(p.out, p.tint(cGray, "(nil)"))
			return
		}
		fmt.Fprintf(p.out, "%q\n", v.Str)
	case '*':
		if v.Null {
			fmt.Fprintln(p.out, p.tint(cGray, "(nil)"))
			return
		}
		if len(v.Array) == 0 {
			fmt.Fprintln(p.out, p.tint(cGray, "(empty array)"))
			return
		}
		// Align the "N)" markers like redis-cli does.
		width := len(strconv.Itoa(len(v.Array)))
		for i, el := range v.Array {
			marker := fmt.Sprintf("%*d)", width, i+1)
			fmt.Fprintf(p.out, "%s%s ", indent, p.tint(cGray, marker))
			// Nested containers start on the next line, indented under the marker.
			if el.Kind == '*' && !el.Null && len(el.Array) > 0 {
				fmt.Fprintln(p.out)
				p.printIndent(el, indent+strings.Repeat(" ", width+2))
			} else {
				p.printIndent(el, indent+strings.Repeat(" ", width+2))
			}
		}
	default:
		fmt.Fprintln(p.out, p.tint(cGray, "(nil)"))
	}
}

func printHelp(color bool) {
	tip := func(cmd, desc string) {
		if color {
			fmt.Printf("  \033[1m%-28s\033[0m %s\n", cmd, desc)
		} else {
			fmt.Printf("  %-28s %s\n", cmd, desc)
		}
	}
	fmt.Println("Cache-Pot CLI — a RESP shell. Any Redis command works, plus:")
	tip("SET / GET / DEL …", "core key/value commands")
	tip("VSET / VSEARCH", "vector store")
	tip("SCACHE.SET / SCACHE.GET", "semantic cache")
	tip("REMEMBER / RECALL", "agent memory")
	fmt.Println("Shell commands:")
	tip("help", "show this help")
	tip("clear", "clear the screen")
	tip("exit / quit", "leave the CLI")
	fmt.Println("Full command reference: https://cache-pot.mintlify.app")
}

// openHistory appends interactive commands to ~/.cache-pot_history. Failures
// are silent — history is a convenience, not a requirement.
func openHistory(interactive bool) *os.File {
	if !interactive {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(home, ".cache-pot_history"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// isTerminal reports whether f is a character device (a TTY) rather than a pipe
// or file, without pulling in a terminal dependency.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
