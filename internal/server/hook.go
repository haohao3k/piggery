package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// hookTimeout bounds one run of a hook, including its child processes.
var hookTimeout = 10 * time.Second

// NotifyHooksDir is where the notify hooks live: every executable file in it is run for each notice.
func NotifyHooksDir(dir string) string { return filepath.Join(dir, "hooks", "notify.d") }

// NotifyHooks are the hooks the daemon runs for a notice: the regular executable files of
// hooks/notify.d, by name. legacy reports a hooks/notify file, which is no longer run (it belongs in
// notify.d); `piggery check` warns about it.
func NotifyHooks(dir string) (files []string, legacy bool) {
	if _, err := os.Lstat(filepath.Join(dir, "hooks", "notify")); err == nil {
		legacy = true
	}
	entries, _ := os.ReadDir(NotifyHooksDir(dir))
	for _, e := range entries {
		if fi, err := os.Stat(filepath.Join(NotifyHooksDir(dir), e.Name())); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			files = append(files, filepath.Join(NotifyHooksDir(dir), e.Name()))
		}
	}
	sort.Strings(files)
	return files, legacy
}

// notifyHook is core's notify sink: run every hook of hooks/notify.d asynchronously and in
// parallel, each with the message as one JSON line on stdin, its own timeout and process group.
// A failure is logged to stderr as JSON (message id, the hook's file name and the error, never the
// body); nothing else depends on it.
func (s *server) notifyHook(messageID string) {
	files, _ := NotifyHooks(s.dir)
	if len(files) == 0 {
		return
	}
	s.hooks.Add(1)
	go func() {
		defer s.hooks.Done()
		mail, err := s.eng.NotifyMail(context.Background(), messageID)
		if err != nil {
			s.log.Error("notify hook", "message", messageID, "err", err)
			return
		}
		line, _ := json.Marshal(mail)
		line = append(line, '\n')
		var wg sync.WaitGroup
		for _, path := range files {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
				defer cancel()
				cmd := exec.CommandContext(ctx, path)
				cmd.Stdin = bytes.NewReader(line)
				// Own process group, so a timeout kills whatever the hook started too.
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
				cmd.WaitDelay = time.Second
				if err := cmd.Run(); err != nil {
					s.log.Error("notify hook", "message", messageID, "hook", filepath.Base(path), "err", err, "timed_out", ctx.Err() != nil)
				}
			}()
		}
		wg.Wait()
	}()
}
