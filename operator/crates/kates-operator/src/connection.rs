//! Where a resource's Kates API and key come from: the Secret `kates-connection` in its own
//! namespace, else the operator's own connection, a file mounted when `connection.fallback` is on.

use std::collections::HashMap;
use std::hash::{DefaultHasher, Hash, Hasher};
use std::path::PathBuf;
use std::sync::Mutex;

use k8s_openapi::api::core::v1::Secret;
use kates_client::{Client, Observer};
use kube::Api;
use secrecy::SecretString;

pub const SECRET: &str = "kates-connection";
pub const FALLBACK_DIR: &str = "/etc/kates-operator/connection";
// Each Secret edit can name a new url, so one namespace could otherwise grow the cache for good.
const MAX_CLIENTS: usize = 256;

pub struct Connection {
    pub api: Client,
    pub key: SecretString,
}

#[derive(Debug, thiserror::Error)]
pub enum Failure {
    #[error("no Secret {SECRET} in this namespace and no operator connection")]
    Missing,
    #[error("{0}")]
    Invalid(String),
    #[error(transparent)]
    Kube(#[from] kube::Error),
}

struct Fields {
    url: String,
    key: String,
    ca_bundle: Option<Vec<u8>>,
}

/// One client per (url, caBundle), rebuilt on a change; the key is read on every pass, so a rotated
/// one needs no restart.
pub struct Connections {
    clients: Mutex<HashMap<(String, u64), Client>>,
    fallback: PathBuf,
    observer: Observer,
}

impl Connections {
    #[must_use]
    pub fn new(fallback: PathBuf, observer: Observer) -> Self {
        Self {
            clients: Mutex::default(),
            fallback,
            observer,
        }
    }

    /// # Errors
    /// `Missing` with neither source, `Invalid` when no client can be built from one.
    pub async fn resolve(
        &self,
        kube: &kube::Client,
        namespace: &str,
    ) -> Result<Connection, Failure> {
        let secrets: Api<Secret> = Api::namespaced(kube.clone(), namespace);
        let fields = match secrets.get_opt(SECRET).await? {
            Some(secret) => from_secret(&secret)?,
            None => self.read_files().await?,
        };
        let api = self.client(&fields)?;
        Ok(Connection {
            api,
            key: SecretString::from(fields.key),
        })
    }

    async fn read_files(&self) -> Result<Fields, Failure> {
        let read = |name: &str| tokio::fs::read(self.fallback.join(name));
        let Ok(key) = read("apiKey").await else {
            return Err(Failure::Missing);
        };
        let url = read("url")
            .await
            .map_err(|_| Failure::Invalid("the operator connection has no url".to_owned()))?;
        Ok(Fields {
            url: text(&url, "url")?,
            key: text(&key, "apiKey")?,
            ca_bundle: read("caBundle").await.ok(),
        })
    }

    fn client(&self, fields: &Fields) -> Result<Client, Failure> {
        let mut hasher = DefaultHasher::new();
        fields.ca_bundle.hash(&mut hasher);
        let cache_key = (fields.url.clone(), hasher.finish());
        let mut clients = self.clients.lock().expect("the client cache");
        if let Some(client) = clients.get(&cache_key) {
            return Ok(client.clone());
        }
        let client = Client::new(&fields.url, fields.ca_bundle.as_deref())
            .map_err(|error| Failure::Invalid(error.to_string()))?
            .observed_by(self.observer.clone());
        if clients.len() >= MAX_CLIENTS {
            clients.clear();
        }
        clients.insert(cache_key, client.clone());
        Ok(client)
    }
}

fn from_secret(secret: &Secret) -> Result<Fields, Failure> {
    let data = secret.data.as_ref();
    let field = |name: &str| data.and_then(|data| data.get(name)).map(|value| &value.0);
    let required = |name: &str| {
        field(name)
            .ok_or_else(|| Failure::Invalid(format!("Secret {SECRET} has no key {name}")))
            .and_then(|value| text(value, name))
    };
    Ok(Fields {
        url: required("url")?,
        key: required("apiKey")?,
        ca_bundle: field("caBundle").cloned(),
    })
}

fn text(value: &[u8], name: &str) -> Result<String, Failure> {
    let text = std::str::from_utf8(value)
        .map_err(|_| Failure::Invalid(format!("{name} is not text")))?
        .trim();
    if text.is_empty() {
        return Err(Failure::Invalid(format!("{name} is empty")));
    }
    Ok(text.to_owned())
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use k8s_openapi::ByteString;

    use super::*;

    fn secret(keys: &[(&str, &str)]) -> Secret {
        let data: BTreeMap<_, _> = keys
            .iter()
            .map(|(k, v)| ((*k).to_owned(), ByteString(v.as_bytes().to_vec())))
            .collect();
        Secret {
            data: Some(data),
            ..Secret::default()
        }
    }

    #[test]
    fn a_secret_needs_a_url_and_a_key() {
        let fields = from_secret(&secret(&[("url", "http://k\n"), ("apiKey", "a")])).unwrap();
        assert_eq!(fields.url, "http://k");
        assert!(matches!(
            from_secret(&secret(&[("url", "http://k")])),
            Err(Failure::Invalid(message)) if message.contains("apiKey")
        ));
        assert!(matches!(
            from_secret(&secret(&[("url", " "), ("apiKey", "a")])),
            Err(Failure::Invalid(_))
        ));
    }

    #[tokio::test]
    async fn no_file_is_no_connection_and_a_rotated_file_is_read_again() {
        let dir = std::env::temp_dir().join(format!("kates-operator-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let connections = Connections::new(dir.clone(), std::sync::Arc::new(|_, _, _| {}));
        assert!(matches!(
            connections.read_files().await,
            Err(Failure::Missing)
        ));

        std::fs::write(dir.join("apiKey"), "first").unwrap();
        std::fs::write(dir.join("url"), "http://k").unwrap();
        assert_eq!(connections.read_files().await.unwrap().key, "first");
        std::fs::write(dir.join("apiKey"), "second").unwrap();
        assert_eq!(connections.read_files().await.unwrap().key, "second");
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn the_client_cache_stays_bounded() {
        let connections = Connections::new(PathBuf::new(), std::sync::Arc::new(|_, _, _| {}));
        for port in 0..=MAX_CLIENTS {
            let fields = Fields {
                url: format!("http://kates:{port}"),
                key: "k".to_owned(),
                ca_bundle: None,
            };
            connections.client(&fields).unwrap();
        }
        assert_eq!(connections.clients.lock().unwrap().len(), 1);
    }
}
