package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowd"
)

// qoqi users: the people who sign in to the console, kept in the store,
// beside the flows. The console starts with admin; the others are added
// here or in the console, through the daemon, from wherever it is.

const usersUsage = `Usage:
  qoqi users ls
  qoqi users add <name> [--password-stdin] [--admin]
  qoqi users passwd <name> [--password-stdin]
  qoqi users rm <name>
                              The people who sign in to the console, through
                              the daemon. A password given here is theirs to
                              change at their next sign in. Without
                              --password-stdin, it is asked for. A user
                              added is an operator, who works with instances;
                              --admin makes one who also deploys code and
                              changes people`

func cmdUsers(rest []string, options map[string]string) {
	if len(rest) == 0 {
		fmt.Println(usersUsage)
		exit(1)
	}
	switch rest[0] {
	case "ls":
		status, data := flowRequest("GET", "/api/users", nil)
		if status >= 400 {
			flowRefused(status, data)
		}
		var users []flowd.User
		if err := json.Unmarshal(data, &users); err != nil {
			fatal(err)
		}
		for _, u := range users {
			renew := ""
			if u.Renew {
				renew = "  (to change their password)"
			}
			role := "admin"
			if u.Operator {
				role = "operator"
			}
			fmt.Printf("%-24s %-9s since %s%s\n", u.Name, role, time.UnixMilli(u.Created).Format("2006-01-02"), renew)
		}
	case "add", "passwd":
		if len(rest) != 2 {
			fmt.Println(usersUsage)
			exit(1)
		}
		name := rest[1]
		if err := flowd.CheckUserName(name); err != nil {
			fatal(err)
		}
		password := askPassword(options["password-stdin"] != "")
		if err := flowd.CheckPasswordStrength(password); err != nil {
			fatal(err)
		}
		path, body := "/api/users", map[string]any{"name": name, "password": password, "admin": options["admin"] != ""}
		if rest[0] == "passwd" {
			path, body = "/api/users/"+url.PathEscape(name)+"/password", map[string]any{"password": password}
		}
		data, _ := json.Marshal(body)
		if status, said := flowRequest("POST", path, data); status >= 400 {
			flowRefused(status, said)
		}
		if rest[0] == "add" {
			fmt.Printf("%s signs in to the console from now on, and chooses a password of their own\n", name)
		} else {
			fmt.Printf("%s has a new password, to change at their next sign in: their sessions are ended\n", name)
		}
	case "rm":
		if len(rest) != 2 {
			fmt.Println(usersUsage)
			exit(1)
		}
		if status, said := flowRequest("DELETE", "/api/users/"+url.PathEscape(rest[1]), nil); status >= 400 {
			flowRefused(status, said)
		}
		fmt.Printf("%s is taken away, and their sessions\n", rest[1])
	default:
		fmt.Println(usersUsage)
		exit(1)
	}
}

// askPassword reads a password: the first line of the input, or typed
// twice at a terminal, without being shown.
func askPassword(fromStdin bool) string {
	in := bufio.NewReader(os.Stdin)
	if fromStdin || !isTerminal(os.Stdin) {
		line, _ := in.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	read := func(prompt string) string {
		fmt.Fprint(os.Stderr, prompt)
		quiet := exec.Command("stty", "-echo")
		quiet.Stdin = os.Stdin
		_ = quiet.Run()
		line, _ := in.ReadString('\n')
		loud := exec.Command("stty", "echo")
		loud.Stdin = os.Stdin
		_ = loud.Run()
		fmt.Fprintln(os.Stderr)
		return strings.TrimRight(line, "\r\n")
	}
	first := read("Password: ")
	if read("Again: ") != first {
		fatal(fmt.Errorf("the two passwords are not the same"))
	}
	return first
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
