package svc

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// This file provides pure-Go uid/gid → name lookups so the panel works
// without cgo and inside minimal containers. The system user creation itself
// is delegated to useradd/chpasswd.

// parsePasswd returns uid -> username.
func parsePasswd() map[int]string {
	out := map[int]string{}
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 3 {
			continue
		}
		if uid, err := strconv.Atoi(fields[2]); err == nil {
			out[uid] = fields[0]
		}
	}
	return out
}

// parseGroup returns gid -> group name.
func parseGroup() map[int]string {
	out := map[int]string{}
	f, err := os.Open("/etc/group")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 3 {
			continue
		}
		if gid, err := strconv.Atoi(fields[2]); err == nil {
			out[gid] = fields[0]
		}
	}
	return out
}

// lookupUID resolves a username to a uid via /etc/passwd.
func lookupUID(name string) (int, error) {
	if name == "" {
		return -1, nil
	}
	for uid, uname := range parsePasswd() {
		if uname == name {
			return uid, nil
		}
	}
	return -1, fmt.Errorf("unknown user %q", name)
}

// lookupGID resolves a group name to a gid via /etc/group.
func lookupGID(name string) (int, error) {
	if name == "" {
		return -1, nil
	}
	for gid, gname := range parseGroup() {
		if gname == name {
			return gid, nil
		}
	}
	return -1, fmt.Errorf("unknown group %q", name)
}

// statIDs extracts uid/gid from a file's stat info.
func statIDs(info os.FileInfo) (uid, gid int) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return -1, -1
}

func lookupUserName(info os.FileInfo) string {
	uid, _ := statIDs(info)
	if name, ok := parsePasswd()[uid]; ok {
		return name
	}
	return strconv.Itoa(uid)
}

func lookupGroupName(info os.FileInfo) string {
	_, gid := statIDs(info)
	if name, ok := parseGroup()[gid]; ok {
		return name
	}
	return strconv.Itoa(gid)
}
