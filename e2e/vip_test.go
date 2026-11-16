//go:build e2e

// Package e2e runs the forwarder in a kind cluster as an HA routing peer for two
// virtual addresses, against a NetBird server and client from NetBird's e2e
// harness. It needs docker, kind, kubectl and helm on the PATH.
//
//	go test -tags e2e -v -timeout 40m ./e2e/
//
// Set NBFWD_E2E_KEEP=1 to leave the cluster and containers running.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/e2e/harness"
	"github.com/netbirdio/netbird/shared/management/http/api"
)

const (
	clusterName = "nbfwd-e2e"
	fwdImage    = "netbird-forwarder:e2e"
	agnhost     = "registry.k8s.io/e2e-test-images/agnhost:2.53"
	vipA        = "10.250.0.10"
	vipB        = "10.250.0.11"
	// managementURL is how pods reach the harness server: kind nodes share its
	// docker network, where it has this alias.
	managementURL = "http://netbird.local:8080"
)

func TestVirtualAddresses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	keep := os.Getenv("NBFWD_E2E_KEEP") != ""

	t.Log("starting the NetBird server")
	srv, err := harness.StartCombined(ctx)
	require.NoError(t, err)
	if !keep {
		t.Cleanup(func() { _ = srv.Terminate(context.Background()) })
	}
	_, err = srv.Bootstrap(ctx)
	require.NoError(t, err)
	nb := srv.API()

	group := func(name string) string {
		g, err := nb.Groups.Create(ctx, api.GroupRequest{Name: name})
		require.NoError(t, err)
		return g.Id
	}
	forwarders, laptops, groupA, groupB := group("forwarders"), group("laptops"), group("vip-a"), group("vip-b")

	setupKey := func(name string, autoGroup string) string {
		ephemeral := true
		k, err := nb.SetupKeys.Create(ctx, api.CreateSetupKeyRequest{
			Name: name, Type: "reusable", AutoGroups: []string{autoGroup}, Ephemeral: &ephemeral, ExpiresIn: 86400,
		})
		require.NoError(t, err)
		return k.Key
	}
	fwdKey, laptopKey := setupKey("forwarders", forwarders), setupKey("laptops", laptops)

	network, err := nb.Networks.Create(ctx, api.NetworkRequest{Name: "e2e"})
	require.NoError(t, err)
	for name, r := range map[string][2]string{"vip-a": {vipA, groupA}, "vip-b": {vipB, groupB}} {
		_, err := nb.Networks.Resources(network.Id).Create(ctx, api.NetworkResourceRequest{
			Name: name, Address: r[0] + "/32", Enabled: true, Groups: []string{r[1]},
		})
		require.NoError(t, err)
	}
	_, err = nb.Networks.Routers(network.Id).Create(ctx, api.NetworkRouterRequest{
		Enabled: true, Masquerade: true, Metric: 9999, PeerGroups: &[]string{forwarders},
	})
	require.NoError(t, err)

	// vip-a: every TCP port, so a port the forwarder does not list still reaches
	// it and must be refused there. vip-b: no policy yet.
	allow := func(name, dst string, ports *[]string) {
		_, err := nb.Policies.Create(ctx, api.PolicyUpdate{
			Name: name, Enabled: true,
			Rules: []api.PolicyRuleUpdate{{
				Name: name, Enabled: true, Action: api.PolicyRuleUpdateActionAccept,
				Protocol: api.PolicyRuleUpdateProtocolTcp, Ports: ports,
				Sources: &[]string{laptops}, Destinations: &[]string{dst},
			}},
		})
		require.NoError(t, err)
	}
	allow("laptops-to-vip-a", groupA, nil)

	k := newKind(t, srv.NetworkName(), keep)
	buildForwarderImage(t, ctx)
	k.loadImage(ctx, fwdImage)
	k.loadImage(ctx, agnhost)
	k.apply(ctx, backend("backend-a")+"\n"+backend("backend-b"))
	k.helmInstall(ctx, chartValues(fwdKey))
	k.run(ctx, "rollout", "status", "deployment/netbird-forwarder", "--timeout=180s")
	k.run(ctx, "rollout", "status", "deployment/backend-a", "deployment/backend-b", "--timeout=180s")

	t.Log("starting the laptop client")
	laptop, err := harness.StartClient(ctx, srv, laptopKey, harness.WithClientName("laptop"))
	require.NoError(t, err)
	if !keep {
		t.Cleanup(func() { _ = laptop.Terminate(context.Background()) })
	}
	require.NoError(t, laptop.WaitConnected(ctx, 2*time.Minute))

	get := func(url string) (string, error) {
		code, out, err := laptop.Exec(ctx, "wget", "-qO-", "-T", "3", url)
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("wget exit %d: %s", code, strings.TrimSpace(out))
		}
		return strings.TrimSpace(out), nil
	}
	eventually := func(what string, timeout time.Duration, check func() error) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			err := check()
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %v\nlaptop status:\n%s", what, err, laptopStatus(ctx, laptop))
			}
			time.Sleep(2 * time.Second)
		}
	}
	served := func(vip, backend string) func() error {
		return func() error {
			out, err := get("http://" + vip + "/hostname")
			if err != nil {
				return err
			}
			if !strings.HasPrefix(out, backend) {
				return fmt.Errorf("%s answered %q, want a %s pod", vip, out, backend)
			}
			return nil
		}
	}

	t.Run("vip-a reaches backend-a", func(t *testing.T) {
		eventually("vip-a", 3*time.Minute, served(vipA, "backend-a"))
	})

	t.Run("unlisted port is refused by the forwarder", func(t *testing.T) {
		_, err := get("http://" + vipA + ":81/")
		require.Error(t, err)
		require.True(t, k.logsContain(ctx, "Refused routed connection from"), "no forwarder logged the refusal")
	})

	t.Run("vip-b needs its own policy", func(t *testing.T) {
		_, err := get("http://" + vipB + "/hostname")
		require.Error(t, err, "vip-b answered without a policy")
		allow("laptops-to-vip-b", groupB, &[]string{"80"})
		eventually("vip-b after its policy", 2*time.Minute, served(vipB, "backend-b"))
	})

	t.Run("failover to the other replica", func(t *testing.T) {
		eventually("vip-a before failover", time.Minute, served(vipA, "backend-a"))
		active := k.activeForwarder(ctx, t, func() {
			for i := 0; i < 3; i++ {
				require.NoError(t, served(vipA, "backend-a")())
			}
		})
		t.Logf("laptop routes through %s; deleting it", active)

		type probe struct {
			worst  time.Duration
			failed int
			total  int
		}
		stop := make(chan struct{})
		result := make(chan probe, 1)
		go func() {
			var p probe
			var down time.Time
			for {
				select {
				case <-stop:
					if !down.IsZero() {
						p.worst = max(p.worst, time.Since(down))
					}
					result <- p
					return
				default:
				}
				p.total++
				if served(vipA, "backend-a")() != nil {
					p.failed++
					if down.IsZero() {
						down = time.Now()
					}
				} else if !down.IsZero() {
					p.worst = max(p.worst, time.Since(down))
					down = time.Time{}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()

		k.run(ctx, "delete", "pod", active, "--wait=false")
		time.Sleep(45 * time.Second)
		close(stop)
		p := <-result

		t.Logf("probes: %d, failed: %d, longest outage: %s", p.total, p.failed, p.worst.Round(100*time.Millisecond))
		require.NoError(t, served(vipA, "backend-a")(), "vip-a is down after the failover window")
		require.Less(t, p.worst, 15*time.Second, "failover took too long")

		now := k.activeForwarder(ctx, t, func() {
			for i := 0; i < 3; i++ {
				require.NoError(t, served(vipA, "backend-a")())
			}
		})
		require.NotEqual(t, active, now)
		t.Logf("laptop now routes through %s", now)
	})
}

func laptopStatus(ctx context.Context, laptop *harness.Client) string {
	_, out, _ := laptop.Exec(ctx, "netbird", "status", "-d")
	return out
}

// buildForwarderImage builds the image from the repository's Dockerfile, the
// same build that CI publishes.
func buildForwarderImage(t *testing.T, ctx context.Context) {
	t.Helper()
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	out, err := exec.CommandContext(ctx, "docker", "build", "-q", "-t", fwdImage,
		"--build-arg", "FORWARDER_VERSION=e2e", root).CombinedOutput()
	require.NoError(t, err, string(out))
}

type kind struct {
	t          *testing.T
	kubeconfig string
}

func newKind(t *testing.T, dockerNetwork string, keep bool) *kind {
	t.Helper()
	k := &kind{t: t, kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	if keep {
		k.kubeconfig = filepath.Join(os.TempDir(), clusterName+".kubeconfig")
	}
	_ = exec.Command("kind", "delete", "cluster", "--name", clusterName).Run()

	config := filepath.Join(t.TempDir(), "kind.yaml")
	kubeletDir := kubeletTmpfsDir(t)
	nodes := "nodes:\n  - role: control-plane\n"
	if kubeletDir != "" {
		nodes += fmt.Sprintf("    extraMounts:\n      - {hostPath: %s, containerPath: /var/lib/kubelet}\n", kubeletDir)
	}
	require.NoError(t, os.WriteFile(config, []byte("kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\n"+nodes), 0o644))

	cmd := exec.Command("kind", "create", "cluster", "--name", clusterName, "--config", config, "--kubeconfig", k.kubeconfig, "--wait", "120s")
	cmd.Env = append(os.Environ(), "KIND_EXPERIMENTAL_DOCKER_NETWORK="+dockerNetwork)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	if !keep {
		t.Cleanup(func() {
			_ = exec.Command("kind", "delete", "cluster", "--name", clusterName).Run()
			if kubeletDir != "" {
				// The kubelet writes as root, so remove the directory from a container.
				_ = exec.Command("docker", "run", "--rm", "-v", filepath.Dir(kubeletDir)+":/d", "alpine:3.24",
					"rm", "-rf", "/d/"+filepath.Base(kubeletDir)).Run()
			}
		})
	} else {
		t.Logf("kind cluster kept; kubeconfig: %s", k.kubeconfig)
	}
	return k
}

// kubeletTmpfsDir returns a tmpfs directory for the node's kubelet when Docker
// keeps its data on ZFS, and "" otherwise. The kubelet's cAdvisor cannot read
// filesystem stats of a ZFS dataset inside the node, and exits with "failed to
// get rootfs info".
func kubeletTmpfsDir(t *testing.T) string {
	t.Helper()
	root, err := exec.Command("docker", "info", "-f", "{{.DockerRootDir}}").Output()
	require.NoError(t, err)
	fsType, err := exec.Command("stat", "-f", "-c", "%T", strings.TrimSpace(string(root))).Output()
	require.NoError(t, err)
	if strings.TrimSpace(string(fsType)) != "zfs" {
		return ""
	}
	dir, err := os.MkdirTemp("/dev/shm", "nbfwd-e2e-kubelet-")
	require.NoError(t, err)
	t.Logf("docker data is on ZFS; the kubelet uses %s", dir)
	return dir
}

func (k *kind) loadImage(ctx context.Context, image string) {
	k.t.Helper()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", image).Run(); err != nil {
		out, err := exec.CommandContext(ctx, "docker", "pull", "-q", image).CombinedOutput()
		require.NoError(k.t, err, string(out))
	}
	out, err := exec.CommandContext(ctx, "kind", "load", "docker-image", image, "--name", clusterName).CombinedOutput()
	require.NoError(k.t, err, string(out))
}

func (k *kind) kubectl(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", k.kubeconfig}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func (k *kind) run(ctx context.Context, args ...string) string {
	k.t.Helper()
	out, err := k.kubectl(ctx, "", args...)
	require.NoError(k.t, err, out)
	return out
}

func (k *kind) apply(ctx context.Context, manifest string) {
	k.t.Helper()
	out, err := k.kubectl(ctx, manifest, "apply", "-f", "-")
	require.NoError(k.t, err, out)
}

func (k *kind) helmInstall(ctx context.Context, values string) {
	k.t.Helper()
	chart, err := filepath.Abs("../charts/netbird-forwarder")
	require.NoError(k.t, err)
	file := filepath.Join(k.t.TempDir(), "values.yaml")
	require.NoError(k.t, os.WriteFile(file, []byte(values), 0o600))
	out, err := exec.CommandContext(ctx, "helm", "install", "netbird-forwarder", chart,
		"--kubeconfig", k.kubeconfig, "-f", file, "--wait", "--timeout", "180s").CombinedOutput()
	require.NoError(k.t, err, string(out))
}

func (k *kind) forwarderPods(ctx context.Context) []string {
	out := k.run(ctx, "get", "pods", "-l", "app.kubernetes.io/name=netbird-forwarder", "--field-selector=status.phase=Running", "-o", "jsonpath={.items[*].metadata.name}")
	return strings.Fields(out)
}

func (k *kind) logsContain(ctx context.Context, needle string) bool {
	for _, pod := range k.forwarderPods(ctx) {
		if out, err := k.kubectl(ctx, "", "logs", pod); err == nil && strings.Contains(out, needle) {
			return true
		}
	}
	return false
}

// activeForwarder returns the replica that serves the laptop: the only one
// whose count of routed connections to vip-a grows while traffic runs.
func (k *kind) activeForwarder(ctx context.Context, t *testing.T, traffic func()) string {
	t.Helper()
	count := func() map[string]int {
		counts := map[string]int{}
		for _, pod := range k.forwarderPods(ctx) {
			out, err := k.kubectl(ctx, "", "logs", pod)
			require.NoError(t, err, out)
			counts[pod] = strings.Count(out, "to "+vipA+":80, dialing target")
		}
		return counts
	}

	before := count()
	traffic()
	var active []string
	for pod, n := range count() {
		if n > before[pod] {
			active = append(active, pod)
		}
	}
	require.Len(t, active, 1, "want exactly one replica to serve the laptop, got %v", active)
	return active[0]
}

// chartValues configures the chart under test: two replicas serving both VIPs.
func chartValues(setupKey string) string {
	return fmt.Sprintf(`
image: {repository: netbird-forwarder, tag: e2e, pullPolicy: Never}
terminationGracePeriodSeconds: 15
netbird:
  managementURL: %q
  setupKey: {value: %q}
routes:
  - {vip: %s, port: 80, target: "backend-a:8080"}
  - {vip: %s, port: 80, target: "backend-b:8080"}
`, managementURL, setupKey, vipA, vipB)
}

func backend(name string) string {
	return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata: {name: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: %[1]s}}
  template:
    metadata: {labels: {app: %[1]s}}
    spec:
      containers:
        - name: netexec
          image: %[2]s
          imagePullPolicy: IfNotPresent
          args: ["netexec", "--http-port=8080"]
---
apiVersion: v1
kind: Service
metadata: {name: %[1]s}
spec:
  selector: {app: %[1]s}
  ports: [{port: 8080}]`, name, agnhost)
}
