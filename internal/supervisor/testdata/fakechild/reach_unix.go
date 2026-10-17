//go:build !windows

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// reachProbe tries the ways a contained native child could act on
// something outside its wall other than the network and a write, and says
// what happened, one line each. Each argument is one attempt:
//
//	unix=<path>     connect to a stream socket on the file system
//	signal=<pid>    ask whether that process may be signalled (signal 0)
//	start=<a|b|c>   start that program with those arguments, wait for it
//	list=<dir>      count the directory's entries
//	null            write to the null device
//	write=<path>    create that file
//	open=<path>     open that file for reading, without taking it as a
//	                controlling terminal and without waiting on it
//	ownsignal       start itself as a child in a process group of its own
//	                and end that group with a signal, as a carrier ends
//	                its worker
//	hold            wait to be ended (what ownsignal starts)
//	quiet=<a|b|c>   start that program with every stream unset, which has
//	                the null device opened for it, for reading and writing
//
// and the platform's own (reachPlatform). The verdict is the parent's; the
// probe only reports, with the error it was given, because a refusal by
// the wall and a failure of the rig are told apart by that error.
func reachProbe(args []string) {
	for _, arg := range args {
		key, value, _ := strings.Cut(arg, "=")
		switch key {
		case "unix":
			if c, err := net.DialTimeout("unix", value, 2*time.Second); err != nil {
				fmt.Printf("unix=refused %v\n", err)
			} else {
				c.Close()
				fmt.Println("unix=connected")
			}
		case "signal":
			pid, _ := strconv.Atoi(value)
			if err := syscall.Kill(pid, 0); err != nil {
				fmt.Printf("signal=refused %s\n", errnoName(err))
			} else {
				fmt.Println("signal=allowed")
			}
		case "start":
			argv := strings.Split(value, "|")
			out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
			var exit *exec.ExitError
			switch {
			case err == nil:
				fmt.Println("start=ran")
			case errors.As(err, &exit):
				// It was started and said no for itself: the wall let the
				// program run, and what it then could not do is its report.
				fmt.Printf("start=ran exit=%d %s\n", exit.ExitCode(), firstLine(out))
			default:
				fmt.Printf("start=refused %v\n", err)
			}
		case "write":
			if err := os.WriteFile(value, []byte("written from inside"), 0o600); err != nil {
				fmt.Printf("write=refused %v\n", err)
			} else {
				fmt.Println("write=written")
			}
		case "open":
			fd, err := syscall.Open(value, syscall.O_RDONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
			if err != nil {
				fmt.Printf("open=refused %s\n", errnoName(err))
			} else {
				syscall.Close(fd)
				fmt.Println("open=opened")
			}
		case "hold":
			time.Sleep(30 * time.Second)
		case "ownsignal":
			self, err := os.Executable()
			if err != nil {
				fmt.Printf("ownsignal=refused %v\n", err)
				break
			}
			child := exec.Command(self, "reach", "hold")
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := child.Start(); err != nil {
				fmt.Printf("ownsignal=refused start: %v\n", err)
				break
			}
			if err := syscall.Kill(-child.Process.Pid, syscall.SIGKILL); err != nil {
				fmt.Printf("ownsignal=refused %s\n", errnoName(err))
				child.Process.Kill()
				child.Wait()
				break
			}
			child.Wait()
			fmt.Println("ownsignal=delivered")
		case "quiet":
			argv := strings.Split(value, "|")
			if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
				fmt.Printf("quiet=refused %v\n", err)
			} else {
				fmt.Println("quiet=ran")
			}
		case "null":
			f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err == nil {
				_, err = f.Write([]byte("discarded"))
				f.Close()
			}
			if err != nil {
				fmt.Printf("null=refused %v\n", err)
			} else {
				fmt.Println("null=written")
			}
		case "list":
			if entries, err := os.ReadDir(value); err != nil {
				fmt.Printf("list=refused %v\n", err)
			} else {
				fmt.Printf("list=%d\n", len(entries))
			}
		case "read":
			// A file's bytes, named by its path: several in one probe.
			if _, err := os.ReadFile(value); err != nil {
				fmt.Printf("read %s=refused %v\n", value, err)
			} else {
				fmt.Printf("read %s=read\n", value)
			}
		case "names":
			if entries, err := os.ReadDir(value); err != nil {
				fmt.Printf("names %s=refused %v\n", value, err)
			} else {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				fmt.Printf("names %s=%s\n", value, strings.Join(names, ","))
			}
		case "readloop":
			// n reads of one file, while something else opens it by its
			// other names: every one must read (count:path).
			count, path, _ := strings.Cut(value, ":")
			n, _ := strconv.Atoi(count)
			failed := false
			for i := range n {
				if _, err := os.ReadFile(path); err != nil {
					fmt.Printf("readloop=refused at %d: %v\n", i, err)
					failed = true
					break
				}
			}
			if !failed {
				fmt.Printf("readloop=%d read\n", n)
			}
		default:
			reachPlatform(key, value)
		}
	}
}

func firstLine(out []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}

// errnoName is the error as the kernel named it, so a parent can tell
// EPERM (the wall) from ESRCH (no such process in this namespace) or
// ENOTTY (not a terminal) without reading a sentence.
func errnoName(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err.Error()
	}
	for name, e := range map[string]syscall.Errno{"EPERM": syscall.EPERM, "EACCES": syscall.EACCES, "ESRCH": syscall.ESRCH,
		"ENOSYS": syscall.ENOSYS, "ENOTTY": syscall.ENOTTY, "ECONNREFUSED": syscall.ECONNREFUSED, "ENOENT": syscall.ENOENT, "ENXIO": syscall.ENXIO,
		"EPROTOTYPE": syscall.EPROTOTYPE} {
		if e == errno {
			return name
		}
	}
	return "errno=" + strconv.Itoa(int(errno))
}

// carrierProbe starts bin/worker.exe under the runtime root with its
// output discarded but for what it prints, and one program of the
// system's, and reports each. Which of the two a wall lets run is the
// platform's to decide and the parent's to judge.
func carrierProbe() string {
	worker := "ran"
	out, err := exec.Command(filepath.Join(os.Getenv("AII_RUNTIME_ROOT"), "bin", "worker.exe"), "reach", "null").Output()
	if err != nil || !strings.Contains(string(out), "null=written") {
		worker = fmt.Sprintf("refused(%v)", err)
	}
	system := "ran"
	if err := exec.Command("/usr/bin/true").Run(); err != nil {
		system = "refused"
	}
	return "worker=" + worker + " system=" + system
}
