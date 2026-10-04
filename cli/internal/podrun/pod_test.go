package podrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func testPod() Pod {
	return Pod{
		Namespace: "kafka",
		Name:      "kates-migrate-target",
		Image:     "quay.io/strimzi/kafka:1.1.0-kafka-4.3.0",
		Labels:    map[string]string{"kates.io/lab": "m282-430", "kates.io/test-pod": "false"},
		Env:       map[string]string{"KAFKA_HEAP_OPTS": "-Xmx256M"},
		SecretEnv: map[string]SecretRef{"TARGET_PW": {Secret: "kates-mm2", Key: "password"}},
	}
}

func TestManifest(t *testing.T) {
	got, err := Manifest(testPod())
	if err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: v1
kind: Pod
metadata:
    name: kates-migrate-target
    namespace: kafka
    labels:
        app.kubernetes.io/part-of: kates
        kates.io/lab: m282-430
        kates.io/podrun: "true"
        kates.io/test-pod: "true"
spec:
    restartPolicy: Never
    terminationGracePeriodSeconds: 10
    automountServiceAccountToken: false
    securityContext:
        runAsNonRoot: true
        seccompProfile:
            type: RuntimeDefault
    containers:
        - name: client
          image: quay.io/strimzi/kafka:1.1.0-kafka-4.3.0
          command:
            - /bin/sh
            - -c
            - 'trap : TERM INT; sleep infinity & wait'
          env:
            - name: LOG_DIR
              value: /tmp
            - name: KAFKA_HEAP_OPTS
              value: -Xmx256M
            - name: TARGET_PW
              valueFrom:
                secretKeyRef:
                    name: kates-mm2
                    key: password
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
                drop:
                    - ALL
          resources:
            requests:
                cpu: 50m
                memory: 64Mi
            limits:
                cpu: 500m
                memory: 512Mi
`
	if got != want {
		t.Errorf("manifest:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "password:") || strings.Contains(got, "secret-value") {
		t.Error("a credential value leaked into the manifest")
	}

	// The fixed labels win over a caller's attempt to unset them.
	var m struct {
		Metadata struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Labels[LabelTestPod] != "true" {
		t.Errorf("%s = %q, want true", LabelTestPod, m.Metadata.Labels[LabelTestPod])
	}
}

func TestManifestOptions(t *testing.T) {
	p := Pod{Namespace: "kafka-m282-430-src", Name: "kates-migrate-source", Image: "apache/kafka:3.9.1",
		ImagePullPolicy: "IfNotPresent", RunAsUser: 1000, Env: map[string]string{"LOG_DIR": "/var/tmp"}}
	got, err := Manifest(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"imagePullPolicy: IfNotPresent\n",
		"runAsUser: 1000\n",
		"- name: LOG_DIR\n              value: /var/tmp\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "name: LOG_DIR") != 1 {
		t.Errorf("LOG_DIR must appear exactly once:\n%s", got)
	}
	if strings.Contains(got, "valueFrom") {
		t.Errorf("no SecretEnv, no valueFrom expected:\n%s", got)
	}
}

func TestPodValidate(t *testing.T) {
	base := testPod()
	tests := []struct {
		name   string
		mutate func(*Pod)
		want   string
	}{
		{"valid", func(*Pod) {}, ""},
		{"no namespace", func(p *Pod) { p.Namespace = "" }, "namespace is required"},
		{"bad namespace", func(p *Pod) { p.Namespace = "Kafka_NS" }, "not a DNS label"},
		{"no name", func(p *Pod) { p.Name = "" }, "name is required"},
		{"bad name", func(p *Pod) { p.Name = "-leading" }, "not a DNS label"},
		{"long name", func(p *Pod) { p.Name = strings.Repeat("a", 64) }, "not a DNS label"},
		{"no image", func(p *Pod) { p.Image = "" }, "image is required"},
		{"negative uid", func(p *Pod) { p.RunAsUser = -1 }, "negative"},
		{"bad env name", func(p *Pod) { p.Env = map[string]string{"1X": "v"} }, "not an identifier"},
		{"bad secret env name", func(p *Pod) { p.SecretEnv = map[string]SecretRef{"A-B": {"s", "k"}} }, "not an identifier"},
		{"incomplete secret ref", func(p *Pod) { p.SecretEnv = map[string]SecretRef{"PW": {Secret: "s"}} }, "needs both"},
		{"env twice", func(p *Pod) {
			p.Env = map[string]string{"PW": "x"}
			p.SecretEnv = map[string]SecretRef{"PW": {"s", "k"}}
		}, "both as a value and from a Secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.mutate(&p)
			err := p.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate = %v, want %q", err, tt.want)
			}
		})
	}
}

const (
	getPhase = "kubectl -n kafka get pod kates-migrate-target --ignore-not-found -o jsonpath={.status.phase}"
	applyPod = "kubectl -n kafka apply -f -"
	waitPod  = "kubectl -n kafka wait --for=condition=Ready pod/kates-migrate-target --timeout=120s"
	delPod   = "kubectl -n kafka delete pod kates-migrate-target --ignore-not-found"
)

func TestEnsure(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		phase string
		calls []string
	}{
		{"absent: create and wait", "", []string{getPhase, applyPod, waitPod}},
		{"running: wait only", "Running", []string{getPhase, waitPod}},
		{"pending: wait only", "Pending\n", []string{getPhase, waitPod}},
		{"succeeded: replace", "Succeeded", []string{getPhase, delPod, applyPod, waitPod}},
		{"failed: replace", "Failed", []string{getPhase, delPod, applyPod, waitPod}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRunner(t).on(getPhase, tt.phase).on(applyPod, "pod/kates-migrate-target created\n").
				on(waitPod, "pod/kates-migrate-target condition met\n").on(delPod, "")
			if err := Ensure(ctx, f, testPod(), 2*time.Minute); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			f.assertCalls(tt.calls...)
			for _, c := range f.calls {
				if c.key() == applyPod {
					want, _ := Manifest(testPod())
					if c.stdin != want {
						t.Errorf("apply received:\n%s\nwant:\n%s", c.stdin, want)
					}
				}
			}
		})
	}
}

func TestEnsureDefaultTimeoutAndErrors(t *testing.T) {
	ctx := context.Background()
	waitDefault := "kubectl -n kafka wait --for=condition=Ready pod/kates-migrate-target --timeout=300s"
	f := newFakeRunner(t).on(getPhase, "Running").on(waitDefault, "")
	if err := Ensure(ctx, f, testPod(), 0); err != nil {
		t.Fatalf("Ensure with zero timeout: %v", err)
	}
	f.assertCalls(getPhase, waitDefault)

	boom := errors.New("connection refused")
	f = newFakeRunner(t).fail(getPhase, boom)
	err := Ensure(ctx, f, testPod(), time.Minute)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "look up pod kafka/kates-migrate-target") {
		t.Errorf("get failure: %v", err)
	}

	f = newFakeRunner(t).on(getPhase, "").fail(applyPod, boom)
	err = Ensure(ctx, f, testPod(), time.Minute)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "create pod") {
		t.Errorf("apply failure: %v", err)
	}

	f = newFakeRunner(t).on(getPhase, "Running").fail("kubectl -n kafka wait --for=condition=Ready pod/kates-migrate-target --timeout=60s", boom)
	err = Ensure(ctx, f, testPod(), time.Minute)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "did not become Ready within 1m0s") {
		t.Errorf("wait failure: %v", err)
	}

	if err := Ensure(ctx, newFakeRunner(t), Pod{}, time.Minute); err == nil {
		t.Error("an invalid pod must not reach kubectl")
	}
}

func TestDelete(t *testing.T) {
	f := newFakeRunner(t).on(delPod, "")
	if err := Delete(context.Background(), f, testPod()); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(delPod)
	if err := Delete(context.Background(), f, Pod{Name: "x"}); err == nil {
		t.Error("Delete without a namespace must fail before kubectl")
	}
}

func TestExecPassesArgvThrough(t *testing.T) {
	ctx := context.Background()
	// Characters a shell would care about go through untouched: there is no
	// shell to care.
	argv := []string{"/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", "krafter-kafka-bootstrap.kafka.svc:9092",
		"--command-config", "/tmp/client.properties", "--list", "--topic", `kates\..*`, "$HOME", "a b", ";", "|"}
	key := "kubectl -n kafka exec kates-migrate-target -c client -- " + strings.Join(argv, " ")
	f := newFakeRunner(t).on(key, "kates.orders\n")
	out, err := Exec(ctx, f, testPod(), argv...)
	if err != nil {
		t.Fatal(err)
	}
	if out != "kates.orders\n" {
		t.Errorf("Exec = %q", out)
	}
	if got := f.calls[0].args; got[len(got)-1] != "|" || got[len(got)-4] != "$HOME" {
		t.Errorf("argv was altered: %q", got)
	}
	if _, err := Exec(ctx, f, testPod()); err == nil {
		t.Error("Exec without a command must fail")
	}
	if _, err := Exec(ctx, f, Pod{Name: "x"}, "true"); err == nil {
		t.Error("Exec without a namespace must fail")
	}
}

func TestExecInputAndWriteFile(t *testing.T) {
	ctx := context.Background()
	producer := "kubectl -n kafka exec -i kates-migrate-target -c client -- /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server b:9092 --topic kates.orders"
	f := newFakeRunner(t).on(producer, "")
	if _, err := ExecInput(ctx, f, testPod(), "r1\nr2\n", "/opt/kafka/bin/kafka-console-producer.sh", "--bootstrap-server", "b:9092", "--topic", "kates.orders"); err != nil {
		t.Fatal(err)
	}
	if f.calls[0].stdin != "r1\nr2\n" {
		t.Errorf("stdin = %q", f.calls[0].stdin)
	}

	props := SASLProperties("SCRAM-SHA-512", "kates-mm2", "s3cret")
	tee := "kubectl -n kafka exec -i kates-migrate-target -c client -- tee /tmp/client.properties"
	f = newFakeRunner(t).on(tee, props)
	if err := WriteFile(ctx, f, testPod(), DefaultPropertiesPath, props); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(tee)
	if f.calls[0].stdin != props {
		t.Errorf("tee received %q", f.calls[0].stdin)
	}
	for _, c := range f.calls {
		if strings.Contains(c.key(), "s3cret") {
			t.Errorf("the password reached a command line: %s", c.key())
		}
	}

	f = newFakeRunner(t).on(tee, props[:5])
	err := WriteFile(ctx, f, testPod(), DefaultPropertiesPath, props)
	if err == nil || !strings.Contains(err.Error(), "wrote 5 bytes") {
		t.Errorf("short write not detected: %v", err)
	}
	if err := WriteFile(ctx, f, testPod(), "", props); err == nil {
		t.Error("WriteFile without a path must fail")
	}
}

func TestFormatTimeout(t *testing.T) {
	tests := map[time.Duration]string{
		0:                      "1s",
		500 * time.Millisecond: "1s",
		90 * time.Second:       "90s",
		10 * time.Minute:       "600s",
	}
	for d, want := range tests {
		if got := formatTimeout(d); got != want {
			t.Errorf("formatTimeout(%s) = %q, want %q", d, got, want)
		}
	}
}
