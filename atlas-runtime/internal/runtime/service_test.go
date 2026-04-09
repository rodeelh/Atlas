package runtime

import "testing"

func TestService_RecordErrorMarksDegraded(t *testing.T) {
	svc := NewService(1984)
	svc.MarkStarted()
	svc.RecordError("boom")

	status := svc.GetStatus(0, 0, 0)
	if status.State != string(StateDegraded) {
		t.Fatalf("state = %q, want %q", status.State, StateDegraded)
	}
	if status.LastError == nil || *status.LastError != "boom" {
		t.Fatalf("last error = %v, want boom", status.LastError)
	}
}

func TestService_MarkStoppedUpdatesStatus(t *testing.T) {
	svc := NewService(1984)
	svc.MarkStarted()
	svc.MarkStopped()

	status := svc.GetStatus(0, 0, 0)
	if status.State != string(StateStopped) {
		t.Fatalf("state = %q, want %q", status.State, StateStopped)
	}
	if status.IsRunning {
		t.Fatal("expected IsRunning to be false after stop")
	}
}
