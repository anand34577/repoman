//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Native Windows service support: `repoman service install` registers this binary with the
// Service Control Manager, which then starts it without a console (see docs/installation.md).

const serviceName = "RepoMan"

func runningAsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

type winService struct{ run func(context.Context) error }

func (s *winService) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				log.Printf("service stopped: %v", err)
				return false, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 35000}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}

func runService(run func(context.Context) error) error {
	return svc.Run(serviceName, &winService{run})
}

func cmdService(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: repoman service install | uninstall | start | stop | status")
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot reach the Service Control Manager (run this from an elevated \"Run as administrator\" prompt): %w", err)
	}
	defer m.Disconnect()

	if args[0] == "install" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.Abs(exe); err != nil {
			return err
		}
		if s, err := m.OpenService(serviceName); err == nil {
			s.Close()
			return errors.New("the RepoMan service is already installed; run `repoman service uninstall` first to reinstall it")
		}
		s, err := m.CreateService(serviceName, exe, mgr.Config{
			DisplayName:      "RepoMan Repository Manager",
			Description:      "Self-hosted artifact repository manager (Maven, npm, PyPI, Docker, Go, APT, YUM, raw).",
			StartType:        mgr.StartAutomatic,
			DelayedAutoStart: true, // wait for the network stack after boot
		}, "serve")
		if err != nil {
			return err
		}
		defer s.Close()
		// restart after a crash: 5s, 30s, then every minute; the failure counter resets daily
		s.SetRecoveryActions([]mgr.RecoveryAction{
			{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
			{Type: mgr.ServiceRestart, Delay: time.Minute},
		}, 86400)
		fmt.Printf("Installed service %q (automatic start).\n", serviceName)
		fmt.Printf("Settings are read from %s\n", filepath.Join(filepath.Dir(exe), "repoman.env"))
		fmt.Println("Start it with:  repoman service start")
		return nil
	}

	s, err := m.OpenService(serviceName)
	if err != nil {
		return errors.New("the RepoMan service is not installed (run `repoman service install` from an elevated prompt)")
	}
	defer s.Close()
	switch args[0] {
	case "uninstall":
		s.Control(svc.Stop) // best effort; deletion completes once it has stopped
		if err := s.Delete(); err != nil {
			return err
		}
		fmt.Println("Service removed. The data directory and repoman.env were left in place.")
	case "start":
		if err := s.Start(); err != nil {
			return err
		}
		fmt.Println("Service started.")
	case "stop":
		if _, err := s.Control(svc.Stop); err != nil {
			return err
		}
		fmt.Println("Stop requested; in-flight requests get up to 30 seconds to finish.")
	case "status":
		st, err := s.Query()
		if err != nil {
			return err
		}
		names := map[svc.State]string{svc.Stopped: "stopped", svc.StartPending: "starting", svc.StopPending: "stopping", svc.Running: "running", svc.Paused: "paused"}
		fmt.Println(names[st.State])
	default:
		return errors.New("usage: repoman service install | uninstall | start | stop | status")
	}
	return nil
}
