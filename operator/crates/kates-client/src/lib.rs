//! The Kates REST API, as the operator uses it. Every answer is classified into [`Error`], so the
//! reconcilers decide on variants and never on status codes.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use reqwest::header::{HeaderMap, RETRY_AFTER};
use reqwest::{Certificate, Method, Response, StatusCode, redirect};
use secrecy::{ExposeSecret, SecretString};
use serde::Deserialize;
use serde_json::Value;

const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
// The CLI's figure, above the 35 s snapshot bound of a first report, so the API's own 503s arrive.
const REQUEST_TIMEOUT: Duration = Duration::from_mins(1);
const MAX_BODY: usize = 8 * 1024 * 1024;

/// Told of every call: method, route template (never the URL), status (0 for a network error).
pub type Observer = Arc<dyn Fn(&'static str, &'static str, u16) + Send + Sync>;

#[derive(Debug, Clone, PartialEq, thiserror::Error)]
pub enum Error {
    /// A 4xx with an `ApiError` body: the request is wrong as written.
    #[error("{message}")]
    Rejected {
        status: u16,
        message: String,
        field_errors: BTreeMap<String, String>,
    },
    #[error("{message}")]
    Conflict { message: String },
    /// 429: the engine is full.
    #[error("{message}")]
    RateLimited {
        retry_after: Option<Duration>,
        message: String,
    },
    #[error("the API refuses this key")]
    InvalidKey,
    #[error("this key lacks the scope for the call")]
    MissingScope,
    /// The run does not exist: only the API's own 404 says so, an ingress's never does.
    #[error("the run does not exist")]
    NotFound,
    /// Everything else: a network error, a proxy's answer, a 5xx. `api_message` is an `ApiError`
    /// body's message.
    #[error("{detail}")]
    Unavailable {
        status: Option<u16>,
        api_message: Option<String>,
        detail: String,
    },
}

impl Error {
    /// The variant's name, the bounded value of the metric's `error` label.
    #[must_use]
    pub fn name(&self) -> &'static str {
        match self {
            Self::Rejected { .. } => "Rejected",
            Self::Conflict { .. } => "Conflict",
            Self::RateLimited { .. } => "RateLimited",
            Self::InvalidKey => "InvalidKey",
            Self::MissingScope => "MissingScope",
            Self::NotFound => "NotFound",
            Self::Unavailable { .. } => "Unavailable",
        }
    }
}

/// A `url` or `caBundle` no client can be built from.
#[derive(Debug, thiserror::Error)]
#[error("{0}")]
pub struct InvalidConnection(String);

#[derive(Debug, Deserialize)]
struct ApiError {
    status: Option<u16>,
    error: Option<String>,
    message: Option<String>,
    #[serde(default, rename = "fieldErrors")]
    field_errors: BTreeMap<String, String>,
}

/// The answer of `/api/drain`. `drained`: the switch is set and nothing is live, so the pod can
/// be replaced without losing a run or leaving a fault in.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Drain {
    pub draining: bool,
    pub drained: bool,
    #[serde(default)]
    pub live_runs: u32,
    #[serde(default)]
    pub faults_in_flight: u32,
    #[serde(default)]
    pub webhooks_in_flight: u32,
}

/// `/api/health`'s `quorum`, as one member sees it.
#[derive(Deserialize, Clone, Debug, Default, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct QuorumView {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub member_id: Option<String>,
    /// It leads, and has committed an entry of its term.
    #[serde(default)]
    pub leader: bool,
    #[serde(default)]
    pub leader_id: Option<String>,
    #[serde(default)]
    pub term: i64,
    /// The last entry it applied; -1 with an empty log, when it is no voter.
    #[serde(default = "no_entry")]
    pub applied_index: i64,
    /// Its health has no `quorum` at all: the backend is older than the quorum.
    #[serde(skip)]
    pub predates: bool,
}

fn no_entry() -> i64 {
    -1
}

/// One Kates API. The key travels per call, so a rotated key needs no new client.
#[derive(Clone)]
pub struct Client {
    http: reqwest::Client,
    base: String,
    observer: Option<Observer>,
}

impl Client {
    /// # Errors
    /// When `url` does not parse, `ca_bundle` holds no certificate, or no client can be built.
    pub fn new(url: &str, ca_bundle: Option<&[u8]>) -> Result<Self, InvalidConnection> {
        let parsed = reqwest::Url::parse(url)
            .map_err(|error| InvalidConnection(format!("url {url:?}: {error}")))?;
        if !matches!(parsed.scheme(), "http" | "https") {
            return Err(InvalidConnection(format!("url {url:?}: not http or https")));
        }
        let mut builder = reqwest::Client::builder()
            .connect_timeout(CONNECT_TIMEOUT)
            .timeout(REQUEST_TIMEOUT)
            .redirect(redirect::Policy::none());
        if let Some(pem) = ca_bundle {
            let certificates = Certificate::from_pem_bundle(pem)
                .map_err(|error| InvalidConnection(format!("caBundle: {error}")))?;
            if certificates.is_empty() {
                return Err(InvalidConnection("caBundle: no certificate".to_owned()));
            }
            builder = builder.tls_certs_merge(certificates);
        }
        let http = builder
            .build()
            .map_err(|error| InvalidConnection(error.to_string()))?;
        Ok(Self {
            http,
            base: url.trim_end_matches('/').to_owned(),
            observer: None,
        })
    }

    #[must_use]
    pub fn observed_by(mut self, observer: Observer) -> Self {
        self.observer = Some(observer);
        self
    }

    /// The member's view of its quorum, from the public `GET /api/health`: no key is sent. A
    /// backend with no quorum says `enabled: false`; one older than the quorum is also `predates`.
    ///
    /// # Errors
    /// See [`Error`].
    pub async fn quorum(&self) -> Result<QuorumView, Error> {
        #[derive(Deserialize)]
        struct Health {
            quorum: Option<QuorumView>,
        }
        let none = SecretString::from(String::new());
        let health: Health = self
            .json(Method::GET, "/api/health", "/api/health", &none, None, None)
            .await?;
        Ok(health.quorum.unwrap_or(QuorumView {
            predates: true,
            ..QuorumView::default()
        }))
    }

    /// `GET /api/drain`.
    ///
    /// # Errors
    /// See [`Error`]. An API older than the switch answers 404.
    pub async fn drain(&self, key: &SecretString) -> Result<Drain, Error> {
        self.json(Method::GET, "/api/drain", "/api/drain", key, None, None)
            .await
    }

    /// `PUT /api/drain`: starts or lifts a drain, and answers the state after it.
    ///
    /// # Errors
    /// See [`Error`]. It needs the admin scope.
    pub async fn set_drain(&self, key: &SecretString, draining: bool) -> Result<Drain, Error> {
        let body = serde_json::json!({"draining": draining});
        self.json(
            Method::PUT,
            "/api/drain",
            "/api/drain",
            key,
            None,
            Some(&body),
        )
        .await
    }

    async fn json<T: serde::de::DeserializeOwned>(
        &self,
        method: Method,
        route: &'static str,
        path: &str,
        key: &SecretString,
        idempotency_key: Option<&str>,
        body: Option<&Value>,
    ) -> Result<T, Error> {
        decode(
            self.call(method, route, path, key, idempotency_key, body)
                .await?,
        )
        .await
    }

    async fn call(
        &self,
        method: Method,
        route: &'static str,
        path: &str,
        key: &SecretString,
        idempotency_key: Option<&str>,
        body: Option<&Value>,
    ) -> Result<Response, Error> {
        let name = method_name(&method);
        let mut request = self
            .http
            .request(method, format!("{}{path}", self.base))
            .bearer_auth(key.expose_secret());
        if let Some(idempotency_key) = idempotency_key {
            request = request.header("Idempotency-Key", idempotency_key);
        }
        if let Some(body) = body {
            request = request.json(body);
        }
        let response = request.send().await;
        let status = response.as_ref().map_or(0, |r| r.status().as_u16());
        if let Some(observer) = &self.observer {
            observer(name, route, status);
        }
        let response = response.map_err(|error| Error::Unavailable {
            status: None,
            api_message: None,
            // reqwest's text names neither the URL nor the headers here, so no key can reach it.
            detail: chain(&error.without_url()),
        })?;
        if response.status().is_success() {
            return Ok(response);
        }
        let status = response.status();
        let headers = response.headers().clone();
        let host = response.url().host_str().unwrap_or_default().to_owned();
        let body = capped(response).await.unwrap_or_default();
        let body = String::from_utf8_lossy(&body);
        Err(classify(status, &headers, &body, &host))
    }
}

// reqwest's own text is "error sending request"; the cause is at the end of its sources.
fn chain(error: &dyn std::error::Error) -> String {
    let mut text = error.to_string();
    let mut source = error.source();
    while let Some(cause) = source {
        text = format!("{text}: {cause}");
        source = cause.source();
    }
    text
}

async fn decode<T: serde::de::DeserializeOwned>(response: Response) -> Result<T, Error> {
    let status = response.status().as_u16();
    let unavailable = |detail| Error::Unavailable {
        status: Some(status),
        api_message: None,
        detail: format!("an answer that is not the API's: {detail}"),
    };
    let body = capped(response).await.map_err(unavailable)?;
    serde_json::from_slice(&body).map_err(|error| unavailable(error.to_string()))
}

// A kates-connection can name any host, and one huge answer would kill the pod every namespace
// shares.
async fn capped(mut response: Response) -> Result<Vec<u8>, String> {
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|error| error.without_url().to_string())?
    {
        if body.len() + chunk.len() > MAX_BODY {
            return Err(format!("over {MAX_BODY} bytes"));
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn method_name(method: &Method) -> &'static str {
    if *method == Method::GET {
        "GET"
    } else if *method == Method::PUT {
        "PUT"
    } else {
        "POST"
    }
}

// Section 4.8 of plans/operator.md; the first match wins. An ingress's HTML 404 on a live run is
// an outage, never a RunGone, so only an ApiError body is believed.
fn classify(status: StatusCode, headers: &HeaderMap, body: &str, host: &str) -> Error {
    let api_error = serde_json::from_str::<ApiError>(body)
        .ok()
        .filter(|error| error.status.is_some() || error.error.is_some());
    let message = || {
        api_error
            .as_ref()
            .and_then(|error| error.message.clone())
            .unwrap_or_default()
    };
    if status == StatusCode::TOO_MANY_REQUESTS {
        return Error::RateLimited {
            retry_after: headers
                .get(RETRY_AFTER)
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.parse().ok())
                .map(Duration::from_secs),
            message: message(),
        };
    }
    let unavailable = |api_message| Error::Unavailable {
        status: Some(status.as_u16()),
        api_message,
        detail: format!("{host} answered {status}"),
    };
    let Some(error) = &api_error else {
        return unavailable(None);
    };
    match status.as_u16() {
        409 => Error::Conflict { message: message() },
        401 => Error::InvalidKey,
        403 if error.error.as_deref() == Some("Invalid API key") => Error::InvalidKey,
        403 => Error::MissingScope,
        404 if error.status == Some(404) && message().starts_with("Test run not found") => {
            Error::NotFound
        }
        400..=499 => Error::Rejected {
            status: status.as_u16(),
            message: message(),
            field_errors: error.field_errors.clone(),
        },
        _ => unavailable(error.message.clone()),
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;
    use wiremock::matchers::{header, method, path};
    use wiremock::{Mock, MockServer, ResponseTemplate};

    use super::*;

    fn key() -> SecretString {
        SecretString::from("the-key")
    }

    fn client(server: &MockServer) -> Client {
        Client::new(&server.uri(), None).unwrap()
    }

    fn api_error(status: u16, error: &str, message: &str) -> ResponseTemplate {
        ResponseTemplate::new(status)
            .set_body_json(json!({"status": status, "error": error, "message": message}))
    }

    async fn answering(response: ResponseTemplate) -> Error {
        let server = MockServer::start().await;
        Mock::given(method("GET"))
            .respond_with(response)
            .mount(&server)
            .await;
        client(&server).drain(&key()).await.unwrap_err()
    }

    #[tokio::test]
    async fn only_a_health_with_no_quorum_at_all_predates_it() {
        let health = |body: serde_json::Value| async move {
            let server = MockServer::start().await;
            Mock::given(method("GET"))
                .and(path("/api/health"))
                .respond_with(ResponseTemplate::new(200).set_body_json(body))
                .mount(&server)
                .await;
            client(&server).quorum().await.unwrap()
        };
        let old = health(json!({"status": "UP"})).await;
        assert!(old.predates && !old.enabled);
        // Configured with no peers, or not started yet: no quorum, but not an old backend.
        let off = health(json!({"status": "UP", "quorum": {"enabled": false}})).await;
        assert!(!off.predates && !off.enabled);
        let on = health(json!({"quorum": {"enabled": true, "leader": true, "term": 3}})).await;
        assert!(!on.predates && on.leader && on.term == 3);
    }

    #[tokio::test]
    async fn set_drain_puts_the_switch_and_reads_the_state_back() {
        let server = MockServer::start().await;
        Mock::given(method("PUT"))
            .and(path("/api/drain"))
            .and(header("authorization", "Bearer the-key"))
            .and(wiremock::matchers::body_json(json!({"draining": true})))
            .respond_with(ResponseTemplate::new(200).set_body_json(json!({
                "draining": true, "liveRuns": 1, "faultsInFlight": 0,
                "webhooksInFlight": 2, "drained": false,
            })))
            .mount(&server)
            .await;
        let drain = client(&server).set_drain(&key(), true).await.unwrap();
        assert_eq!(
            drain,
            Drain {
                draining: true,
                drained: false,
                live_runs: 1,
                faults_in_flight: 0,
                webhooks_in_flight: 2,
            }
        );
    }

    #[tokio::test]
    async fn the_engine_full_answer_carries_retry_after() {
        let error = answering(
            api_error(429, "Too Many Requests", "engine full").insert_header("Retry-After", "30"),
        )
        .await;
        assert_eq!(
            error,
            Error::RateLimited {
                retry_after: Some(Duration::from_secs(30)),
                message: "engine full".to_owned()
            }
        );
    }

    #[tokio::test]
    async fn only_the_apis_own_404_means_the_run_is_gone() {
        let gone = answering(api_error(404, "Not Found", "Test run not found: r")).await;
        assert_eq!(gone, Error::NotFound);

        let other = answering(api_error(404, "Not Found", "HTTP 404 Not Found")).await;
        assert_eq!(other.name(), "Rejected");

        let ingress = answering(ResponseTemplate::new(404).set_body_string("<html>")).await;
        assert_eq!(ingress.name(), "Unavailable");
    }

    #[tokio::test]
    async fn refused_keys_are_told_apart() {
        let invalid = answering(api_error(403, "Invalid API key", "m")).await;
        assert_eq!(invalid, Error::InvalidKey);
        let unauthorized = answering(api_error(401, "Unauthorized", "m")).await;
        assert_eq!(unauthorized, Error::InvalidKey);
        let scope = answering(api_error(403, "Forbidden", "m")).await;
        assert_eq!(scope, Error::MissingScope);
    }

    #[tokio::test]
    async fn field_errors_and_conflicts_keep_the_apis_message() {
        let rejected = answering(
            ResponseTemplate::new(400).set_body_json(json!({"status": 400, "error":
                "Validation Failed", "message": "bad", "fieldErrors": {"spec.acks": "no"}})),
        )
        .await;
        let Error::Rejected { field_errors, .. } = rejected else {
            panic!("{rejected:?}");
        };
        assert_eq!(field_errors["spec.acks"], "no");

        let conflict = answering(api_error(409, "Conflict", "Test is not running")).await;
        assert_eq!(
            conflict,
            Error::Conflict {
                message: "Test is not running".to_owned()
            }
        );
    }

    #[tokio::test]
    async fn a_503_keeps_the_apis_message_and_a_proxy_has_none() {
        let api = answering(api_error(503, "Service Unavailable", "kubernetes down")).await;
        let Error::Unavailable { api_message, .. } = api else {
            panic!("{api:?}");
        };
        assert_eq!(api_message.as_deref(), Some("kubernetes down"));

        let proxy = answering(ResponseTemplate::new(503).set_body_string("upstream")).await;
        let Error::Unavailable { api_message, .. } = proxy else {
            panic!("{proxy:?}");
        };
        assert_eq!(api_message, None);
    }

    #[tokio::test]
    async fn an_answer_over_the_cap_is_an_outage() {
        let error =
            answering(ResponseTemplate::new(200).set_body_bytes(vec![b' '; MAX_BODY + 1])).await;
        assert_eq!(error.name(), "Unavailable");
        assert!(error.to_string().contains("over"), "{error}");
    }

    #[tokio::test]
    async fn a_redirect_is_an_outage_and_not_followed() {
        let error = answering(ResponseTemplate::new(302).insert_header("Location", "/x")).await;
        assert_eq!(error.name(), "Unavailable");
    }

    #[tokio::test]
    async fn the_observer_gets_the_route_template() {
        let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
        let sink = seen.clone();
        let server = MockServer::start().await;
        Mock::given(method("GET"))
            .respond_with(
                ResponseTemplate::new(200)
                    .set_body_json(json!({"draining": false, "drained": false})),
            )
            .mount(&server)
            .await;
        client(&server)
            .observed_by(Arc::new(move |m, r, s| {
                sink.lock().unwrap().push((m, r, s));
            }))
            .drain(&key())
            .await
            .unwrap();
        assert_eq!(*seen.lock().unwrap(), [("GET", "/api/drain", 200)]);
    }

    #[tokio::test]
    async fn a_network_error_says_why() {
        let client = Client::new("http://127.0.0.1:1", None).unwrap();
        let error = client.drain(&key()).await.unwrap_err();
        let Error::Unavailable { status, detail, .. } = error else {
            panic!("{error:?}");
        };
        assert_eq!(status, None);
        assert!(detail.to_lowercase().contains("refused"), "{detail}");
    }

    #[test]
    fn a_bad_url_or_bundle_is_an_invalid_connection() {
        assert!(Client::new("not a url", None).is_err());
        assert!(Client::new("ftp://kates", None).is_err());
        assert!(Client::new("https://kates", Some(b"no certificate")).is_err());
    }
}
