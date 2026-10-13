package idle

import "testing"

func TestCountExecProcs(t *testing.T) {
	titles := []string{"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD"}
	row := func(pid, ppid string) []string { return []string{"1000", pid, ppid, "0", "", "?", "", "x"} }

	// 100 is the main process (its parent, the runtime shim 90, isn't in the
	// container). 101/102 are its workers; 200 is an exec'd shell running
	// composer (201); 300 is a health check.
	rows := [][]string{row("100", "90"), row("101", "100"), row("102", "101"), row("200", "95"), row("201", "200"), row("300", "96")}
	n, err := countExecProcs(titles, rows, 100)
	if err != nil || n != 3 {
		t.Fatalf("countExecProcs = %d, %v; want 3 (shell, composer, health check)", n, err)
	}

	if n, _ := countExecProcs(titles, rows[:3], 100); n != 0 {
		t.Fatalf("server and its workers alone counted %d exec'd processes", n)
	}
	if _, err := countExecProcs([]string{"PID", "CMD"}, rows, 100); err == nil {
		t.Fatal("missing PPID column wasn't reported")
	}
}
