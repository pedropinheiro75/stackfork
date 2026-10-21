package cli

import (
	"os"
	"strings"
	"testing"
)

// down removes an endpoint whose container is gone, and only on this
// project's networks.
func TestDownDisconnectsStaleEndpoints(t *testing.T) {
	log, root, stateDir, project, _ := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_NETWORKS", project+"_default\n")
	t.Setenv("DOCKER_FAKE_NET_ENDPOINTS", project+"-app-1\nproxy\n")
	t.Setenv("DOCKER_FAKE_CONTAINERS", "proxy")
	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := fakeLog(log)
	for _, want := range []string{
		"network ls --filter label=com.docker.compose.project=" + project,
		"network disconnect -f " + project + "_default " + project + "-app-1",
		"network rm " + project + "_default",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "disconnect -f "+project+"_default proxy") {
		t.Errorf("a live container must stay connected:\n%s", got)
	}
}

const staleMsg = "Error response from daemon: failed to set up container networking: endpoint with name %s-app-1 already exists in network %s"

func upCount(log string) int { return strings.Count(fakeLog(log), " up -d ") }

func TestUpRetriesOnceAfterStaleEndpoint(t *testing.T) {
	log, root, stateDir, project, _ := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_NETWORKS", project+"_default\n")
	t.Setenv("DOCKER_FAKE_UP_MSG", strings.NewReplacer("%s-app-1", project+"-app-1", "%s", project+"_default").Replace(staleMsg))
	os.Truncate(log, 0)
	stdout, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	stderr += stdout // a recorded run may print the step output on either stream
	if upCount(log) != 2 || !strings.Contains(fakeLog(log), "network disconnect -f "+project+"_default "+project+"-app-1") {
		t.Fatalf("expected disconnect and one retry:\n%s", fakeLog(log))
	}
	if !strings.Contains(stderr, "stale") {
		t.Fatalf("expected a note, stderr=%s", stderr)
	}
}

func TestUpReportsSecondFailureAfterRetry(t *testing.T) {
	log, root, stateDir, project, _ := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_NETWORKS", project+"_default\n")
	t.Setenv("DOCKER_FAKE_UP_ALWAYS", "1")
	t.Setenv("DOCKER_FAKE_UP_MSG", strings.NewReplacer("%s-app-1", project+"-app-1", "%s", project+"_default").Replace(staleMsg))
	os.Truncate(log, 0)
	_, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code == 0 {
		t.Fatal("expected a failure")
	}
	if upCount(log) != 2 || strings.Count(fakeLog(log), "network disconnect") != 1 {
		t.Fatalf("expected exactly one retry:\n%s", fakeLog(log))
	}
}

func TestUpNeverTouchesOtherProjectsNetworks(t *testing.T) {
	log, root, stateDir, _, _ := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_NETWORKS", "") // the network is not this project's
	t.Setenv("DOCKER_FAKE_UP_MSG", "endpoint with name other-app-1 already exists in network other_default")
	os.Truncate(log, 0)
	if _, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code == 0 {
		t.Fatal("expected a failure")
	}
	if upCount(log) != 1 || strings.Contains(fakeLog(log), "network disconnect") {
		t.Fatalf("expected no disconnect and no retry:\n%s", fakeLog(log))
	}
}

func TestUpKeepsEndpointOfRunningContainer(t *testing.T) {
	log, root, stateDir, project, _ := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_NETWORKS", project+"_default\n")
	t.Setenv("DOCKER_FAKE_CONTAINERS", project+"-app-1")
	t.Setenv("DOCKER_FAKE_UP_MSG", "endpoint with name "+project+"-app-1 already exists in network "+project+"_default")
	os.Truncate(log, 0)
	if _, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code == 0 {
		t.Fatal("expected a failure")
	}
	if upCount(log) != 1 || strings.Contains(fakeLog(log), "network disconnect") {
		t.Fatalf("expected no disconnect and no retry:\n%s", fakeLog(log))
	}
}
