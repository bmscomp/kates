//! The operator's clients for the Kates APIs it installs, one per URL.

use std::collections::HashMap;
use std::sync::Mutex;

use kates_client::{Client, InvalidConnection, Observer};

// A cluster may hold many Kates resources; the cache must not grow for good.
const MAX_CLIENTS: usize = 256;

pub struct Connections {
    clients: Mutex<HashMap<String, Client>>,
    observer: Observer,
}

impl Connections {
    #[must_use]
    pub fn new(observer: Observer) -> Self {
        Self {
            clients: Mutex::default(),
            observer,
        }
    }

    /// A client for a URL the operator builds itself, such as a `Kates` Service's.
    ///
    /// # Errors
    /// When no client can be built from `url`.
    ///
    /// # Panics
    /// If another thread panicked while holding the cache.
    pub fn client_for(&self, url: &str) -> Result<Client, InvalidConnection> {
        let mut clients = self.clients.lock().expect("the client cache");
        if let Some(client) = clients.get(url) {
            return Ok(client.clone());
        }
        let client = Client::new(url, None)?.observed_by(self.observer.clone());
        if clients.len() >= MAX_CLIENTS {
            clients.clear();
        }
        clients.insert(url.to_owned(), client.clone());
        Ok(client)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_client_cache_stays_bounded() {
        let connections = Connections::new(std::sync::Arc::new(|_, _, _| {}));
        for port in 0..=MAX_CLIENTS {
            connections
                .client_for(&format!("http://kates:{port}"))
                .unwrap();
        }
        assert_eq!(connections.clients.lock().unwrap().len(), 1);
    }
}
