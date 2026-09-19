{{/*
The preflight probe: a shell function, `probe ALIAS BOOTSTRAP MODE MECHANISM
USERNAME SECRET_DIR`, that runs one ApiVersions handshake with the workers'
own Kafka client and classifies the failure as PROTOCOL, DNS, TLS, AUTH,
LISTENER or NETWORK. It counts failures in $FAILED and expects $WORK.

Call with (dict "noun" "source" "version" "<Kafka version>"); the noun names
what is probed in the messages. Emits lines at column 0; the caller indents.
*/}}
{{- define "kafka-common.preflight.probe" -}}
{{- $noun := .noun | default "cluster" -}}
{{- $version := .version -}}
# One probe, four verdicts. There is deliberately no separate
# DNS step: `getent` is not in the ubi-minimal image these
# workers run on, and a probe that fails on its own tooling
# reports every {{ $noun }} as unresolvable. The ApiVersions handshake
# already exercises name resolution, TCP, TLS, authentication and
# the protocol floor in that order, and each of those fails with
# a distinguishable exception — so the handshake is run once and
# its output classified.
#
# mode: plaintext | sasl | tls | sasl-tls
#   tls modes have no truststore to hand, so they use the JVM's
#   default trust. Against an internal CA that fails the TLS
#   handshake — which is still proof the broker is reachable and
#   speaking TLS, and is reported as exactly that, not as a pass.
probe() {   # alias, bootstrap, mode, mechanism, username, secret_dir
  ALIAS="$1"; BOOTSTRAP="$2"; MODE="$3"; MECH="$4"; USER="$5"; SDIR="$6"
  echo ""
  echo "══ {{ $noun }}: $ALIAS ── $BOOTSTRAP  [$MODE]"

  CFG="$WORK/$ALIAS.properties"
  : > "$CFG"
  # custom authentication (OAuth and other SASL mechanisms) is
  # configured by properties this probe cannot assemble; the
  # handshake still runs without it, which verifies everything
  # up to the credentials.
  NOCRED=""
  case "$MODE" in
    custom)     MODE="plaintext"; NOCRED="custom" ;;
    custom-tls) MODE="tls"; NOCRED="custom" ;;
  esac
  if [ "$NOCRED" = "custom" ]; then
    echo "  ⏭  CREDENTIAL custom authentication — this probe cannot configure it;"
    echo "               probing without it; the credentials are NOT verified"
  fi
  case "$MODE" in
    plaintext) echo "security.protocol=PLAINTEXT" >> "$CFG" ;;
    sasl)      echo "security.protocol=SASL_PLAINTEXT" >> "$CFG" ;;
    tls)       echo "security.protocol=SSL" >> "$CFG" ;;
    sasl-tls)  echo "security.protocol=SASL_SSL" >> "$CFG" ;;
  esac
  # A SASL {{ $noun }} whose Secret is not here yet — secretSync copies
  # it AFTER install, so on a first install this is the normal
  # case, not a fault. The handshake still runs, without
  # credentials: ApiVersions is answered before SASL begins, so
  # name resolution, reachability and the protocol floor are all
  # still verified. Only the credentials are not, and the verdict
  # says so.
  if [ -n "$MECH" ]; then
    if [ ! -s "$SDIR/password" ]; then
      NOCRED="yes"
      case "$MODE" in
        sasl)     MODE="plaintext" ;;
        sasl-tls) MODE="tls" ;;
      esac
      : > "$CFG"
      case "$MODE" in
        plaintext) echo "security.protocol=PLAINTEXT" >> "$CFG" ;;
        tls)       echo "security.protocol=SSL" >> "$CFG" ;;
      esac
      echo "  ⏭  CREDENTIAL not present yet (secretSync copies it after install) —"
      echo "               probing without it; the credentials are NOT verified this time"
    else
      PW=$(sed 's/[\\"]/\\&/g' "$SDIR/password")
      echo "sasl.mechanism=$MECH" >> "$CFG"
      if [ "$MECH" = "PLAIN" ]; then
        printf '%s\n' "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"$USER\" password=\"$PW\";" >> "$CFG"
      else
        printf '%s\n' "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"$USER\" password=\"$PW\";" >> "$CFG"
      fi
    fi
  fi
  # Bounded: the default 60s request timeout multiplied by retries
  # would outlast the Job's own deadline on an unreachable host.
  echo "request.timeout.ms=15000" >> "$CFG"
  echo "default.api.timeout.ms=20000" >> "$CFG"

  OUT="$WORK/$ALIAS.out"
  /opt/kafka/bin/kafka-broker-api-versions.sh \
    --bootstrap-server "$BOOTSTRAP" --command-config "$CFG" > "$OUT" 2>&1
  RC=$?

  if [ $RC -eq 0 ]; then
    echo "  ✅ HANDSHAKE broker answered ApiVersions to a {{ $version }} client"
    head -1 "$OUT" | sed 's/^/               /'
    return
  fi

  # Classify. ORDER MATTERS, and it is the order the exceptions
  # nest in, not the order of likelihood: the generic NetworkClient
  # line "Connection to node -1 ... failed authentication due to:
  # SSL handshake failed" would match a naive NETWORK test, and
  # its "failed authentication" would match a naive AUTH test, so
  # the specific TLS wording is checked before either of those.
  if grep -qi 'UNSUPPORTED_VERSION\|UnsupportedVersionException' "$OUT"; then
    echo "  ❌ PROTOCOL  UNSUPPORTED_VERSION — this broker predates the Kafka 2.1"
    echo "               floor that {{ $version }} clients enforce (KIP-896)."
    echo "               There is no flag for this. Migrate via an intermediate 3.x cluster."
    FAILED=$((FAILED + 1))
  elif grep -qi 'UnknownHostException\|Failed to resolve\|No resolvable bootstrap' "$OUT"; then
    echo "  ❌ DNS       the bootstrap host does not resolve"
    echo "               check the namespace and cluster domain in bootstrapServers"
    FAILED=$((FAILED + 1))
  elif grep -qi 'SSL handshake failed\|SSLHandshakeException\|PKIX\|valid certification path\|CertificateException\|SslAuthenticationException' "$OUT"; then
    case "$MODE" in
      tls)
        echo "  ⏭  TLS       reachable and speaking TLS, but the certificate is not"
        echo "               trusted by this probe (it has no truststore). Trust and"
        echo "               credentials were NOT verified — the workers will use"
        echo "               tls.trustedCertificateSecret, which this probe does not." ;;
      *)
        echo "  ❌ TLS       the broker answered with a TLS handshake on a listener"
        echo "               configured here as plaintext — set tls.enabled"
        FAILED=$((FAILED + 1)) ;;
    esac
  elif grep -qi 'SaslAuthenticationException\|invalid credentials\|Authentication failed' "$OUT"; then
    echo "  ❌ AUTH      the broker rejected the credentials — the {{ $noun }}-side user,"
    echo "               its password Secret, or the SASL mechanism"
    FAILED=$((FAILED + 1))
  elif grep -qi 'terminated during authentication\|disconnected during authentication' "$OUT"; then
    if [ -n "$NOCRED" ]; then
      echo "  ⏭  AUTH      the listener requires authentication and the credentials"
      echo "               were not available to this probe. Name resolution,"
      echo "               reachability and the protocol floor are verified; the"
      echo "               credentials will be checked on the next upgrade."
    else
      echo "  ❌ LISTENER  the broker closed the connection during authentication:"
      echo "               this port expects TLS and/or a SASL mechanism that the"
      echo "               {{ $noun }}'s tls / authentication settings do not configure"
      FAILED=$((FAILED + 1))
    fi
  elif grep -qi 'Timed out\|TimeoutException\|Connection refused\|Connection to node' "$OUT"; then
    echo "  ❌ NETWORK   resolvable, not reachable — NetworkPolicy, a wrong port,"
    echo "               or a listener that is not advertised on this network"
    FAILED=$((FAILED + 1))
  else
    echo "  ❌ HANDSHAKE failed (exit $RC) for a reason this probe does not classify"
    FAILED=$((FAILED + 1))
  fi
  sed 's/^/               /' "$OUT" | grep -v '^ *$' | head -6
}
{{- end }}
