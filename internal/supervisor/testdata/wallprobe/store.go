//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func storeProbe() {
	fmt.Println("caps=" + tokenCapabilities())
	reads := func(prefix string) {
		for i := 0; ; i++ {
			path := os.Getenv("WALL_READ_" + strconv.Itoa(i))
			if path == "" {
				return
			}
			fmt.Printf("%s%d=%s\n", prefix, i, tryRead(path))
		}
	}
	reads("read")
	if late := os.Getenv("WALL_LATE"); late != "" {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Lstat(late); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Println("late=" + tryRead(late))
	}
	fmt.Println("waiting")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "again" {
			reads("again")
			fmt.Println("done")
			return
		}
	}
	fmt.Println("done")
}

// tokenCapabilities is the token's capability SIDs (TokenCapabilities).
func tokenCapabilities() string {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "error:" + err.Error()
	}
	defer token.Close()
	const tokenCapabilities = 30 // TOKEN_INFORMATION_CLASS.TokenCapabilities
	var n uint32
	_ = windows.GetTokenInformation(token, tokenCapabilities, nil, 0, &n)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(token, tokenCapabilities, &buf[0], n, &n); err != nil {
		return "error:" + err.Error()
	}
	groups := (*windows.Tokengroups)(unsafe.Pointer(&buf[0]))
	var out []string
	for _, g := range groups.AllGroups() {
		out = append(out, g.Sid.String())
	}
	return strings.Join(out, ",")
}
