# Observabilité

TrainPilot peut exposer des métriques Prometheus et les profils Go `pprof`.
Ces fonctions sont optionnelles et désactivées par défaut.

## Configuration

```json
{
  "diagnostics": {
    "enabled": false,
    "listen": "127.0.0.1:6060",
    "metrics": true,
    "pprof": false
  }
}
```

`enabled` démarre le listener de diagnostic. Sa valeur par défaut est `false`.
`listen` vaut `127.0.0.1:6060` par défaut. Il doit différer de `http.listen`.
`metrics` active `/metrics`. Sa valeur par défaut est `true`.
`pprof` active `/debug/pprof/`. Sa valeur par défaut est `false`.

Le listener de diagnostic utilise son propre serveur et son propre mux HTTP.
Il ne modifie pas le contrat de l'API publique.

## Sécurité

Les endpoints de diagnostic ne demandent pas d'authentification applicative.
Le bind par défaut est donc limité à l'interface loopback.

Ne pas utiliser une adresse publique sans pare-feu ou tunnel sécurisé.
Les profils `pprof` peuvent contenir des données présentes dans la mémoire du
processus. Leur activation doit rester temporaire et contrôlée.

Aucun endpoint de diagnostic n'est enregistré sur le listener de l'API.
Aucun ID de locomotive, utilisateur, session, route ou lease n'est utilisé
comme label Prometheus. Les tokens et le texte SQL ne sont jamais des labels.

## Métriques

Les métriques runtime et processus standard sont exposées sous les préfixes
`go_*` et `process_*`. Certaines métriques processus, comme les descripteurs de
fichiers, dépendent de la plateforme.

### HTTP

- `trainpilot_http_requests_total`
- `trainpilot_http_requests_in_flight`
- `trainpilot_http_request_duration_seconds`
- `trainpilot_http_request_size_bytes`
- `trainpilot_http_response_size_bytes`

Les labels HTTP sont la méthode, la route templatisée et la classe de statut.
Une valeur telle que `/api/v1/locomotives/{id}` est utilisée à la place de
l'URL réelle.

### WebSocket

- `trainpilot_websocket_connections`
- `trainpilot_websocket_connections_total`
- `trainpilot_websocket_events_total`
- `trainpilot_websocket_events_dropped_total`
- `trainpilot_websocket_queue_overflows_total`
- `trainpilot_websocket_snapshot_requests_total`
- `trainpilot_websocket_snapshot_generation_duration_seconds`
- `trainpilot_websocket_snapshot_size_bytes`

Un overflow ferme la connexion afin de forcer une resynchronisation complète.
Le serveur ne maintient pas d'identité de connexion durable. Une reconnexion ne
peut donc pas être distinguée sûrement d'une nouvelle connexion. Le compteur
`trainpilot_websocket_connections_total` fournit le nombre total accepté.

### Feedback

- `trainpilot_feedback_events_total`
- `trainpilot_feedback_processing_duration_seconds`
- `trainpilot_feedback_mapping_errors_total`
- `trainpilot_feedback_occupancy_updates_total`

Les providers sont normalisés vers `simulator`, `z21-rbus`, `dccex` ou `other`.
Les adresses des capteurs ne sont jamais des labels.

### Contrôle, itinéraires et accessoires

- `trainpilot_control_leases_active`
- `trainpilot_control_lease_operations_total`
- `trainpilot_control_commands_total`
- `trainpilot_control_lease_stop_duration_seconds`
- `trainpilot_control_safety_stops_total`
- `trainpilot_route_operations_total`
- `trainpilot_turnout_commands_total`
- `trainpilot_turnout_confirmations_total`

Les résultats possibles sont bornés. Ils distinguent notamment les succès,
refus, conflits, timeouts et erreurs.

### Centrale

- `trainpilot_station_state`
- `trainpilot_station_state_changes_total`
- `trainpilot_station_reconnections_total`
- `trainpilot_station_commands_total`
- `trainpilot_station_command_duration_seconds`

`trainpilot_station_state` est un gauge one-hot pour `online`, `degraded`,
`offline` et `unknown`. Les commandes sont observées sans ajout de retry.

### SQLite

- `trainpilot_store_operations_total`
- `trainpilot_store_operation_duration_seconds`
- `trainpilot_store_transactions_total`
- `trainpilot_sqlite_file_size_bytes`
- `trainpilot_sqlite_connections_in_use`
- `trainpilot_sqlite_wait_count_total`
- `trainpilot_sqlite_wait_duration_seconds_total`

Les opérations sont identifiées par un nom logique borné. La taille du fichier
principal et du WAL est lue au moment du scrape. Aucun contenu SQL n'est exposé.

## Vérification avec curl

Activer les diagnostics puis démarrer le serveur :

```bash
go run ./cmd/dccd serve --config config.json
curl --fail http://127.0.0.1:6060/metrics
```

Les URLs suivantes doivent rester absentes du port public :

```bash
curl -i http://127.0.0.1:8080/metrics
curl -i http://127.0.0.1:8080/debug/pprof/
```

## Scrape Prometheus

Prometheus doit idéalement tourner sur une autre machine que le serveur mesuré.

```yaml
scrape_configs:
  - job_name: trainpilot
    scrape_interval: 5s
    static_configs:
      - targets: ["trainpilot-host:6060"]
```

Si Prometheus est distant, configurer explicitement `diagnostics.listen` et
protéger le port par le réseau. Ne pas exposer ce port sur Internet.

## Captures pprof

Activer temporairement `diagnostics.pprof`, puis utiliser :

```bash
curl --fail --output heap.pb.gz http://127.0.0.1:6060/debug/pprof/heap
go tool pprof heap.pb.gz

curl --fail --output cpu.pb.gz 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30'
go tool pprof cpu.pb.gz

curl --fail --output trace.out 'http://127.0.0.1:6060/debug/pprof/trace?seconds=5'
go tool trace trace.out
```

Désactiver `pprof` après la capture.

Pour la collecte externe et les dashboards Grafana de benchmark, voir
`docs/BENCHMARK-MONITORING.md`.
