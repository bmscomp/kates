//! The Kates REST API, as the operator uses it. Every answer is classified into [`Error`], so the
//! reconcilers decide on variants and never on status codes.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use reqwest::header::{HeaderMap, LOCATION, RETRY_AFTER};
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

/// `TestResult.TaskStatus`; a status this build does not know reads as still running.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RunStatus {
    Pending,
    Running,
    Done,
    Failed,
}

impl RunStatus {
    fn parse(status: &str) -> Self {
        match status {
            "PENDING" => Self::Pending,
            "DONE" => Self::Done,
            "FAILED" => Self::Failed,
            _ => Self::Running,
        }
    }

    #[must_use]
    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Done | Self::Failed)
    }
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireRun {
    id: String,
    status: String,
    client_key: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Run {
    pub id: String,
    pub status: RunStatus,
    pub client_key: Option<String>,
}

impl From<WireRun> for Run {
    fn from(run: WireRun) -> Self {
        Self {
            status: RunStatus::parse(&run.status),
            id: run.id,
            client_key: run.client_key,
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct Violation {
    pub metric: String,
    pub threshold: f64,
    pub actual: f64,
    pub reason: Option<String>,
}

impl std::fmt::Display for Violation {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match &self.reason {
            Some(reason) => write!(f, "{}: {reason}", self.metric),
            None => write!(
                f,
                "{}: {} against {}",
                self.metric, self.actual, self.threshold
            ),
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct SlaVerdict {
    pub passed: bool,
    #[serde(default)]
    pub violations: Vec<Violation>,
}

/// What the operator reads of `GET /api/tests/{id}/report`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Report {
    #[serde(default)]
    pub summary: Value,
    pub overall_sla_verdict: Option<SlaVerdict>,
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct ComplianceSummary {
    pub pass: i64,
    pub fail: i64,
    pub warn: i64,
    pub waived: i64,
    pub unknown: i64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ComplianceResult {
    pub rule: String,
    pub status: String,
    pub detail: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ComplianceReport {
    pub verdict: String,
    #[serde(default)]
    pub summary: ComplianceSummary,
    #[serde(default)]
    pub results: Vec<ComplianceResult>,
}

/// An answered check. `check_id` is absent when the API ran it but could not store the report.
#[derive(Debug, Clone)]
pub struct ComplianceAnswer {
    pub report: ComplianceReport,
    pub check_id: Option<String>,
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

    /// `GET /api/health`'s `capabilities`; an API older than the field lists none.
    ///
    /// # Errors
    /// See [`Error`].
    pub async fn capabilities(&self, key: &SecretString) -> Result<Vec<String>, Error> {
        #[derive(Deserialize)]
        struct Health {
            #[serde(default)]
            capabilities: Vec<String>,
        }
        let health: Health = self
            .json(Method::GET, "/api/health", "/api/health", key, None, None)
            .await?;
        Ok(health.capabilities)
    }

    /// Starts a run, or finds the one `idempotency_key` already started.
    ///
    /// # Errors
    /// See [`Error`].
    pub async fn create_test(
        &self,
        key: &SecretString,
        idempotency_key: &str,
        request: &Value,
    ) -> Result<Run, Error> {
        let run: WireRun = self
            .json(
                Method::POST,
                "/api/tests",
                "/api/tests",
                key,
                Some(idempotency_key),
                Some(request),
            )
            .await?;
        Ok(run.into())
    }

    /// # Errors
    /// See [`Error`].
    pub async fn get_test(&self, key: &SecretString, id: &str) -> Result<Run, Error> {
        let path = format!("/api/tests/{id}");
        let run: WireRun = self
            .json(Method::GET, "/api/tests/{id}", &path, key, None, None)
            .await?;
        Ok(run.into())
    }

    /// The run `client_key` created. An API older than the filter lists everything, so an item
    /// counts only if it carries the key itself.
    ///
    /// # Errors
    /// See [`Error`].
    pub async fn find_by_client_key(
        &self,
        key: &SecretString,
        client_key: &str,
    ) -> Result<Option<Run>, Error> {
        #[derive(Deserialize)]
        struct Page {
            #[serde(default)]
            items: Vec<WireRun>,
        }
        let path = format!("/api/tests?clientKey={client_key}");
        let page: Page = self
            .json(Method::GET, "/api/tests", &path, key, None, None)
            .await?;
        Ok(page
            .items
            .into_iter()
            .find(|run| run.client_key.as_deref() == Some(client_key))
            .map(Run::from))
    }

    /// # Errors
    /// `Conflict` when the run is not running. See [`Error`].
    pub async fn cancel_test(&self, key: &SecretString, id: &str) -> Result<(), Error> {
        let path = format!("/api/tests/{id}/cancel");
        let _: Value = self
            .json(
                Method::POST,
                "/api/tests/{id}/cancel",
                &path,
                key,
                None,
                None,
            )
            .await?;
        Ok(())
    }

    /// # Errors
    /// See [`Error`].
    pub async fn report(&self, key: &SecretString, id: &str) -> Result<Report, Error> {
        let path = format!("/api/tests/{id}/report");
        self.json(
            Method::GET,
            "/api/tests/{id}/report",
            &path,
            key,
            None,
            None,
        )
        .await
    }

    /// Runs a compliance check. A stored report's id comes from the `Location` header.
    ///
    /// # Errors
    /// See [`Error`].
    pub async fn check_compliance(
        &self,
        key: &SecretString,
        reference: &Value,
    ) -> Result<ComplianceAnswer, Error> {
        const ROUTE: &str = "/api/conformity/check";
        let response = self
            .call(Method::POST, ROUTE, ROUTE, key, None, Some(reference))
            .await?;
        let check_id = response
            .headers()
            .get(LOCATION)
            .and_then(|location| location.to_str().ok())
            .and_then(|location| location.rsplit('/').next())
            .filter(|id| !id.is_empty())
            .map(str::to_owned);
        let report = decode(response).await?;
        Ok(ComplianceAnswer { report, check_id })
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
    use wiremock::matchers::{header, method, path, query_param};
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
        client(&server).get_test(&key(), "r").await.unwrap_err()
    }

    #[tokio::test]
    async fn create_sends_the_key_and_the_idempotency_key() {
        let server = MockServer::start().await;
        Mock::given(method("POST"))
            .and(path("/prefix/api/tests"))
            .and(header("authorization", "Bearer the-key"))
            .and(header("idempotency-key", "uid-1"))
            .respond_with(ResponseTemplate::new(202).set_body_json(
                json!({"id": "r1", "status": "PENDING", "clientKey": "uid-1", "results": []}),
            ))
            .expect(1)
            .mount(&server)
            .await;

        let url = format!("{}/prefix/", server.uri());
        let run = Client::new(&url, None)
            .unwrap()
            .create_test(&key(), "uid-1", &json!({"type": "LOAD"}))
            .await
            .unwrap();
        assert_eq!(run.id, "r1");
        assert_eq!(run.status, RunStatus::Pending);
        assert_eq!(run.client_key.as_deref(), Some("uid-1"));
    }

    #[tokio::test]
    async fn an_older_api_that_lists_everything_finds_nothing() {
        let server = MockServer::start().await;
        Mock::given(method("GET"))
            .and(path("/api/tests"))
            .and(query_param("clientKey", "uid-1"))
            .respond_with(ResponseTemplate::new(200).set_body_json(
                json!({"items": [{"id": "other", "status": "RUNNING"}], "total": 1}),
            ))
            .mount(&server)
            .await;
        let found = client(&server).find_by_client_key(&key(), "uid-1").await;
        assert_eq!(found, Ok(None));
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
    async fn the_check_id_comes_from_location_and_a_lost_report_has_none() {
        let body = json!({"verdict": "FAIL", "summary": {"pass": 1, "fail": 1, "warn": 0,
            "waived": 0, "unknown": 0}, "results": [{"rule": "kafka.replicas.min",
            "status": "FAIL", "detail": "2 < 3"}]});
        let server = MockServer::start().await;
        Mock::given(method("POST"))
            .and(path("/api/conformity/check"))
            .respond_with(
                ResponseTemplate::new(200)
                    .set_body_json(&body)
                    .insert_header("Location", "/api/conformity/checks/c1"),
            )
            .up_to_n_times(1)
            .mount(&server)
            .await;
        Mock::given(method("POST"))
            .and(path("/api/conformity/check"))
            .respond_with(ResponseTemplate::new(200).set_body_json(&body))
            .mount(&server)
            .await;

        let client = client(&server);
        let stored = client.check_compliance(&key(), &json!({})).await.unwrap();
        assert_eq!(stored.check_id.as_deref(), Some("c1"));
        assert_eq!(stored.report.summary.fail, 1);
        let lost = client.check_compliance(&key(), &json!({})).await.unwrap();
        assert_eq!(lost.check_id, None);
    }

    #[tokio::test]
    async fn the_observer_gets_the_route_template_and_never_the_url() {
        let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
        let sink = seen.clone();
        let server = MockServer::start().await;
        Mock::given(method("GET"))
            .respond_with(
                ResponseTemplate::new(200)
                    .set_body_json(json!({"id": "secret-id", "status": "DONE"})),
            )
            .mount(&server)
            .await;
        client(&server)
            .observed_by(Arc::new(move |m, r, s| {
                sink.lock().unwrap().push((m, r, s));
            }))
            .get_test(&key(), "secret-id")
            .await
            .unwrap();
        assert_eq!(*seen.lock().unwrap(), [("GET", "/api/tests/{id}", 200)]);
    }

    #[tokio::test]
    async fn a_network_error_says_why() {
        let client = Client::new("http://127.0.0.1:1", None).unwrap();
        let error = client.get_test(&key(), "r").await.unwrap_err();
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
