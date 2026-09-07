# TrainPilot command reference

This document covers the commands provided by the repository's four binaries:

- `dccd`: server and local user administration;
- `dccctl`: interactive client for the TrainPilot API;
- `dcc-api-conformance`: validation of an instance's contract;
- `trainpilot-bench`: reproducible remote load generator.

HTTP endpoints are not duplicated here. Their reference remains
`api/openapi.yaml`. Development commands and test scripts are documented in
`docs/TESTING.md`.

The examples use installed binaries. During development, replace them with
`go run ./cmd/dccd`, `go run ./cmd/dccctl`, or
`go run ./cmd/dcc-api-conformance`, or `go run ./cmd/trainpilot-bench`.

## Safety precautions

The following commands can act on real hardware:

- `dccctl throttle` and `dccctl function`;
- `dccctl power on` and `dccctl power off`;
- `dccctl emergency-stop`;
- `dccctl turnout` with a position;
- `dcc-api-conformance --allow-active-commands`;
- `dcc-api-conformance --check-turnouts`.

Only use them with an explicitly selected command station.
Use the simulator for automated tests.

## Variables used in the examples

```bash
export TRAINPILOT_URL='http://127.0.0.1:8080'
export DCC_PASSWORD='correct-horse-1'
export DCC_DISPATCHER_PASSWORD='correct-horse-dispatcher'
export DCC_ADMIN_PASSWORD='correct-horse-admin'
export DCCD_SOCKET='/tmp/dccd-admin.sock'
```

Do not store real passwords in the shell history.

## `dccd`

### `dccd serve`

Starts the HTTP server, the administration Unix socket, and the configured
command station. The diagnostics listener is also started when enabled.

```bash
dccd serve --config config.json
```

Options:

- `--config <file>`: load a JSON configuration file;
- without `--config`: use the built-in default values.

### Common `dccd user` options

User commands communicate through the local Unix socket. The server must be
running.

- `--socket <path>`: administration socket; defaults to
  `/tmp/dccd-admin.sock`;
- `--username <name>`: target user;
- `--display-name <name>`: display name used when creating a user;
- `--role <role>`: `viewer`, `driver`, `dispatcher`, or
  `administrator`;
- `--must-change`: require a password change;
- `--password-stdin`: read the password from standard input.

### `dccd user bootstrap`

Creates the first user. This command is rejected as soon as a user already
exists.

```bash
printf '%s\n' "$DCC_ADMIN_PASSWORD" | dccd user bootstrap \
  --socket "$DCCD_SOCKET" \
  --username admin \
  --display-name 'Administrator' \
  --role administrator \
  --password-stdin
```

### `dccd user add`

Adds an enabled user.

```bash
printf '%s\n' "$DCC_PASSWORD" | dccd user add \
  --socket "$DCCD_SOCKET" \
  --username alice \
  --display-name 'Alice' \
  --role driver \
  --password-stdin
```

Add `--must-change` to require a new password at the next login.

### `dccd user list`

Lists users, their roles, and whether they are enabled.

```bash
dccd user list --socket "$DCCD_SOCKET"
```

### `dccd user enable`

Re-enables a disabled user.

```bash
dccd user enable --socket "$DCCD_SOCKET" --username alice
```

### `dccd user disable`

Disables a user and revokes their active sessions.

```bash
dccd user disable --socket "$DCCD_SOCKET" --username alice
```

### `dccd user role`

Changes a user's role.

```bash
dccd user role \
  --socket "$DCCD_SOCKET" \
  --username alice \
  --role dispatcher
```

### `dccd user passwd`

Replaces the password and revokes the user's sessions.

```bash
printf '%s\n' "$DCC_PASSWORD" | dccd user passwd \
  --socket "$DCCD_SOCKET" \
  --username alice \
  --password-stdin \
  --must-change
```

## `dccctl`

### Authentication and global options

Every operational command requires `--username`.

- `--server <URL>`: server URL; defaults to `http://127.0.0.1:8080`;
- `--username <name>`: API user; required;
- `--password-env <variable>`: variable containing the password;
- `--state-file <file>`: local session and lease storage.

If `--password-env` is absent, `dccctl` prompts for the password. The
password is never written to the state file. Tokens and leases are stored
there with `0600` permissions.

The common prefix used below is:

```bash
dccctl --server "$TRAINPILOT_URL" --username alice --password-env DCC_PASSWORD
```

### `dccctl locomotives`

Lists locomotives with their ID, DCC address, and name.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD locomotives
```

### `dccctl locomotive-show`

Displays all properties of a locomotive.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD locomotive-show loco-bb26001
```

### `dccctl locomotive-add`

Adds a locomotive. The `administrator` role is required.

Syntax:

```text
locomotive-add <name> <dcc-address> [short|long] [14|28|128] [manufacturer] [model]
```

Example:

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  locomotive-add 'BB 26001' 3 short 128 Jouef 'BB 26000'
```

The address type is inferred when its argument is omitted.

### `dccctl locomotive-update`

Updates a locomotive. The `administrator` role is required. A locomotive
with an active lease cannot be updated.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  locomotive-update loco-bb26001 'Refurbished BB 26001' 3 short 128 Jouef 'BB 26000'
```

### `dccctl locomotive-delete`

Deletes a locomotive. The `administrator` role is required. A locomotive
referenced by the lease history cannot be deleted.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD locomotive-delete loco-test
```

### `dccctl acquire`

Acquires exclusive control of a locomotive. The lease is stored in the local
state file.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD acquire loco-bb26001
```

### `dccctl throttle`

Sets the speed and direction. A lease stored by `acquire` is required.
The speed must be between 0 and 100. The default direction is `forward`.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD throttle loco-bb26001 40 forward
```

A speed of `0` is a priority stop command.

### `dccctl function`

Enables or disables a locomotive function. A lease is required.
The number must be between 0 and 68, subject to the command station's
capabilities.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD function loco-bb26001 0 true
```

Use `false` to disable the function.

### `dccctl release`

Requests a controlled stop, then releases the lease.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD release loco-bb26001
```

### `dccctl power status`

Displays the known connectivity, power, emergency-stop, and telemetry state
of the command station.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD power status
```

### `dccctl power on`

Enables track power. No lease is required.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD power on
```

After an emergency stop, this command permits active commands again when it
succeeds.

### `dccctl power off`

Disables track power. This safety command has priority.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD power off
```

### `dccctl emergency-stop`

Sends a global emergency stop. This command has priority.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD emergency-stop
```

### `dccctl turnouts`

Lists turnouts and their operational state.

```bash
dccctl --server "$TRAINPILOT_URL" --username dispatcher \
  --password-env DCC_DISPATCHER_PASSWORD turnouts
```

### `dccctl turnout --positions`

Lists the logical positions declared for a turnout.

```bash
dccctl --server "$TRAINPILOT_URL" --username dispatcher \
  --password-env DCC_DISPATCHER_PASSWORD turnout turnout-1 --positions
```

### `dccctl turnout <id> <position>`

Commands a logical position. The `dispatcher` or `administrator` role is
required. Only declared positions are accepted.

```bash
dccctl --server "$TRAINPILOT_URL" --username dispatcher \
  --password-env DCC_DISPATCHER_PASSWORD turnout turnout-1 diverging
```

### `dccctl export-rolling-stock`

Exports rolling stock to an archive. The file is written with `0600`
permissions. An existing file is replaced.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD export-rolling-stock rolling-stock.zip
```

### `dccctl import-rolling-stock`

Imports a rolling-stock archive. The `administrator` role is required.
By default, data is merged.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-rolling-stock rolling-stock.zip
```

Add `--replace` to replace the existing library. This option is destructive
and may be rejected when leases are active.

### `dccctl export-layout`

Exports blocks, feedback mappings, turnouts, and routes.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD export-layout layout.zip
```

### `dccctl import-layout`

Imports a layout archive. The `administrator` role is required. By default,
data is merged.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-layout layout.zip
```

Add `--replace` to replace the existing configuration.

### Help and shell completion

Display general help or help for a command:

```bash
dccctl --help
dccctl help throttle
```

Generate completion scripts for `bash`, `fish`, `powershell`, or `zsh`:

```bash
dccctl --username alice --password-env DCC_PASSWORD completion bash > dccctl.bash
dccctl --username alice --password-env DCC_PASSWORD completion fish > dccctl.fish
dccctl --username alice --password-env DCC_PASSWORD completion powershell > dccctl.ps1
dccctl --username alice --password-env DCC_PASSWORD completion zsh > _dccctl
```

The completion command currently inherits global initialization. It therefore
requires a username and a valid session.

## `trainpilot-bench`

The benchmark process is designed to run on a different machine from the
server. See `docs/BENCHMARKING.md` for profile and credential formats.

### `trainpilot-bench validate-profile`

Validates a versioned YAML profile and its referenced fixture without
contacting a server.

```bash
trainpilot-bench validate-profile benchmarks/profiles/smoke.yaml
```

Use `--fixture <file>` to override the fixture declared by the profile.

### `trainpilot-bench run`

Runs warm-up and measured phases, prints a summary, and writes a versioned JSON
report.

```bash
trainpilot-bench run \
  --server http://192.168.1.20:8080 \
  --profile benchmarks/profiles/smoke.yaml \
  --credentials benchmark-credentials.json \
  --allow-active-commands \
  --allow-simulator-api \
  --output benchmarks/results/simulator-smoke.json
```

Credential files must have `0600` permissions on Unix. As an alternative, pass
one or more environment mappings:

```bash
trainpilot-bench run \
  --profile benchmarks/profiles/smoke.yaml \
  --credential benchmark-01=TRAINPILOT_BENCH_PASSWORD \
  --allow-active-commands \
  --allow-simulator-api \
  --output benchmarks/results/simulator-smoke.json
```

Options:

- `--server <URL>`: target server; defaults to `http://127.0.0.1:8080`;
- `--profile <file>`: versioned YAML profile; required;
- `--fixture <file>`: override the profile's fixture;
- `--credentials <file>`: protected JSON credentials file;
- `--credential <user=ENV>`: environment-backed account; repeatable;
- `--duration <duration>`: override the measured duration;
- `--seed <integer>`: override the deterministic seed;
- `--output <file>`: JSON report path; required;
- `--allow-active-commands`: allow commands that can affect a railway;
- `--allow-simulator-api`: allow simulator test-event injection;
- `--allow-real-hardware`: additionally confirm active commands against a
  non-simulator driver.

The root command also provides `--version`, `help`, and generated completion
commands for `bash`, `fish`, `powershell`, and `zsh`.

`--credentials` and `--credential` are mutually exclusive. Active profiles are
rejected unless explicitly enabled. A non-simulator target requires both
active-command and real-hardware confirmation.

## `dcc-api-conformance`

This binary verifies the public contract of a running server. It returns a
non-zero status code when at least one check fails.

### Passive checks

The default mode checks health, versions, authentication, read operations,
structured errors, and exports. It does not command the track.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2'
```

This mode creates and revokes test sessions.

### Endpoint inventory

Displays every public endpoint and its conformance class. No server is
contacted.

```bash
dcc-api-conformance --list-endpoints
```

### Active commands

Adds track-power, lease, speed, and function checks.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --allow-active-commands
```

Only use this on an explicitly selected test instance.

### Configuration mutations

Adds CRUD checks and temporary imports. An administrator account is required.
Use a disposable database.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --admin admin --admin-pass "$DCC_ADMIN_PASSWORD" \
  --allow-configuration-mutations
```

### Turnout checks

Commands declared positions and verifies their confirmations. An administrator
account is required.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --admin admin --admin-pass "$DCC_ADMIN_PASSWORD" \
  --check-turnouts
```

This option is active even without `--allow-active-commands`.

### Session expiration

Checks the natural expiration of access and refresh tokens. Use short TTLs on
a dedicated instance.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --check-session-expiration \
  --session-expiration-max-wait 15s
```

The maximum duration defaults to `15s`. It avoids waiting for production
TTLs.

### Complete option list

- `--server <URL>`: target server;
- `--user1`, `--pass1`: first driver account;
- `--user2`, `--pass2`: second driver account;
- `--admin`, `--admin-pass`: account used by administrative checks;
- `--allow-active-commands`: allow track commands;
- `--allow-configuration-mutations`: allow temporary mutations;
- `--check-turnouts`: command and verify turnouts;
- `--check-session-expiration`: verify natural expirations;
- `--session-expiration-max-wait <duration>`: limit each wait;
- `--list-endpoints`: display the inventory, then exit.

This tool receives passwords as command-line arguments. Only use dedicated
test accounts.
