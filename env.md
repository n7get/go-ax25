# go-ax25 Viper Environment Configuration

This project uses `spf13/viper` with:

- Env prefix: `GOAX25`
- Key mapping: `.` -> `_`
- Resolution order: runtime `Set` > environment variable > INI file > schema default

Example mapping:

- `beacon.source` -> `GOAX25_BEACON_SOURCE`
- `kiss.server.max_clients` -> `GOAX25_KISS_SERVER_MAX_CLIENTS`

## Environment Variables

### agwpe.client

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_AGWPE_CLIENT_HOST` | `agwpe.client.host` | `localhost` | Non-empty hostname or IP | AGWPE client host |
| `GOAX25_AGWPE_CLIENT_PORT` | `agwpe.client.port` | `8000` | Integer `1-65535` | AGWPE client port |
| `GOAX25_AGWPE_CLIENT_READ_BUF` | `agwpe.client.read_buf` | `4132` | Integer bytes (`0` uses library default; negative invalid) | AGWPE client rx read buffer size (bytes) |
| `GOAX25_AGWPE_CLIENT_TX_QUEUE_DEPTH` | `agwpe.client.tx_queue_depth` | `8` | Integer (`0` uses library default; negative invalid) | AGWPE client TX channel depth |

### agwpe.server

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_AGWPE_SERVER_ENABLED` | `agwpe.server.enabled` | `true` | Boolean (`true`, `false`, `1`, `0`, `t`, `f`) | Enable AGWPE TCP server |
| `GOAX25_AGWPE_SERVER_ADDR` | `agwpe.server.addr` | `:8000` | Non-empty TCP listen address (for example `:8000`, `127.0.0.1:8000`) | AGWPE TCP listen address |
| `GOAX25_AGWPE_SERVER_MAX_CLIENTS` | `agwpe.server.max_clients` | `16` | Integer `>= 0` (`0` = unlimited) | AGWPE TCP server max simultaneous clients (0 = unlimited) |
| `GOAX25_AGWPE_SERVER_READ_BUF` | `agwpe.server.read_buf` | `4132` | Integer bytes (`<= 0` falls back to server default) | AGWPE server rx read buffer size (bytes) |
| `GOAX25_AGWPE_SERVER_TX_QUEUE_DEPTH` | `agwpe.server.tx_queue_depth` | `64` | Integer (`<= 0` falls back to server default) | AGWPE server TX channel depth |
| `GOAX25_AGWPE_SERVER_MAX_CONNS` | `agwpe.server.max_conns` | `4` | Integer (`<= 0` falls back to server default) | AGWPE server max simultaneous AX.25 connections |

### bbs

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_BBS_CALLSIGN` | `bbs.callsign` | `N0CALL-2` | AX.25 address: callsign `A-Z0-9` (1-6 chars), optional `-SSID` (0-15) | BBS station callsign |
| `GOAX25_BBS_GREETING` | `bbs.greeting` | `Welcome to Go AX.25 BBS` | String | Banner text on connect |
| `GOAX25_BBS_PROMPT` | `bbs.prompt` | `BBS> ` | String | Command prompt |
| `GOAX25_BBS_SYSOP_NAME` | `bbs.sysop_name` | `SYSOP` | String | Sysop display name |
| `GOAX25_BBS_VERSION` | `bbs.version` | `go-ax25-bbs 0.1` | String | Version string reported to callers |
| `GOAX25_BBS_MAX_MESSAGES` | `bbs.max_messages` | `500` | Integer `>= 1` | Maximum number of stored messages |
| `GOAX25_BBS_MAX_BODY_LEN` | `bbs.max_body_len` | `102400` | Integer bytes `>= 1` | Maximum message body size (bytes) |
| `GOAX25_BBS_SYSOP_SECRET` | `bbs.sysop_secret` | `` | Empty, or string min 16 chars | SYSOP challenge-response secret (empty disables SYSOP auth) |
| `GOAX25_BBS_SYSOP_CHALLENGE_TIMEOUT_S` | `bbs.sysop_challenge_timeout_s` | `300` | Integer seconds `>= 1` | SYSOP challenge timeout (seconds) |
| `GOAX25_BBS_SYSOP_SESSION_TIMEOUT_S` | `bbs.sysop_session_timeout_s` | `600` | Integer seconds `>= 1` | SYSOP session idle timeout (seconds) |
| `GOAX25_BBS_SYSOP_LOCKOUT_S` | `bbs.sysop_lockout_s` | `900` | Integer seconds `>= 1` | SYSOP lockout duration after max failed attempts (seconds) |
| `GOAX25_BBS_SYSOP_MAX_ATTEMPTS` | `bbs.sysop_max_attempts` | `3` | Integer `>= 1` | SYSOP max failed auth attempts before lockout |
| `GOAX25_BBS_DB_PATH` | `bbs.db_path` | `bbs.db` | File path string | SQLite database file path |
| `GOAX25_BBS_HOST` | `bbs.host` | `localhost` | Non-empty hostname or IP | AGWPE server host |
| `GOAX25_BBS_PORT` | `bbs.port` | `8000` | Integer `1-65535` | AGWPE server port |

### beacon

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_BEACON_SOURCE` | `beacon.source` | `` | Empty, or AX.25 address: callsign `A-Z0-9` (1-6 chars), optional `-SSID` (0-15) | Beacon source callsign (empty = disabled) |
| `GOAX25_BEACON_DESTINATION` | `beacon.destination` | `BEACON` | AX.25 address: callsign `A-Z0-9` (1-6 chars), optional `-SSID` (0-15) | Beacon destination callsign |
| `GOAX25_BEACON_ADDR` | `beacon.addr` | `` | Empty, or TCP address `host:port` | Beacon KISS TCP target address host:port (overrides kiss.client host/port) |
| `GOAX25_BEACON_VIA` | `beacon.via` | `` | Empty, or comma-separated AX.25 addresses | Comma-separated digipeater path |
| `GOAX25_BEACON_TEXT` | `beacon.text` | `go-ax25` | String (supports `\\r`, `\\n`, `\\t`, `\\xHH`) | Beacon text (supports \\r \\n \\xHH escapes) |
| `GOAX25_BEACON_EVERY` | `beacon.every` | `0` | Integer minutes (`<= 0` disables beacon) | Beacon interval in minutes (0 = disabled) |

### conn

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_CONN_T1_MS` | `conn.t1_ms` | `10000` | Integer milliseconds | T1 acknowledgement timeout (ms) |
| `GOAX25_CONN_T2_MS` | `conn.t2_ms` | `1000` | Integer milliseconds | T2 response delay timeout (ms) |
| `GOAX25_CONN_T3_MS` | `conn.t3_ms` | `180000` | Integer milliseconds | T3 inactive link timeout (ms) |
| `GOAX25_CONN_N2_RETRIES` | `conn.n2_retries` | `10` | Integer | N2 maximum retry count |
| `GOAX25_CONN_WINDOW_SIZE` | `conn.window_size` | `4` | Integer `1-7` (other values are coerced to `4`) | k: maximum outstanding I-frames (1-7) |

### digi

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_DIGI_CALLSIGN` | `digi.callsign` | `` | Empty, or AX.25 address: callsign `A-Z0-9` (1-6 chars), optional `-SSID` (0-15) | Digipeater callsign (empty = disabled) |

### kiss.client

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_KISS_CLIENT_ENABLED` | `kiss.client.enabled` | `false` | Boolean (`true`, `false`, `1`, `0`, `t`, `f`) | Enable KISS TCP client PHY (mutually exclusive with serial PHY) |
| `GOAX25_KISS_CLIENT_HOST` | `kiss.client.host` | `localhost` | Non-empty hostname or IP | KISS TCP client host |
| `GOAX25_KISS_CLIENT_PORT` | `kiss.client.port` | `8100` | Integer `1-65535` | KISS TCP client port |
| `GOAX25_KISS_CLIENT_READ_BUF` | `kiss.client.read_buf` | `4096` | Integer bytes (`0` uses library default; negative invalid) | KISS TCP client rx read buffer size (bytes) |
| `GOAX25_KISS_CLIENT_TX_QUEUE_DEPTH` | `kiss.client.tx_queue_depth` | `8` | Integer (`0` uses library default; negative invalid) | KISS TCP client TX channel depth |

### kiss.serial

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_KISS_SERIAL_ENABLED` | `kiss.serial.enabled` | `false` | Boolean (`true`, `false`, `1`, `0`, `t`, `f`) | Enable serial KISS PHY (mutually exclusive with KISS TCP client) |
| `GOAX25_KISS_SERIAL_DEVICE` | `kiss.serial.device` | `/dev/ttyUSB0` | Serial device path | Serial device for KISS TNC |
| `GOAX25_KISS_SERIAL_BAUD` | `kiss.serial.baud` | `9600` | Integer baud rate | Serial baud rate |
| `GOAX25_KISS_SERIAL_READ_BUF` | `kiss.serial.read_buf` | `1024` | Integer bytes (`<= 0` falls back to PHY default) | KISS serial rx read buffer size (bytes) |
| `GOAX25_KISS_SERIAL_RX_QUEUE_DEPTH` | `kiss.serial.rx_queue_depth` | `64` | Integer (`<= 0` falls back to PHY default) | KISS serial rx frame queue depth |
| `GOAX25_KISS_SERIAL_TX_QUEUE_DEPTH` | `kiss.serial.tx_queue_depth` | `32` | Integer (`<= 0` falls back to PHY default) | KISS serial tx frame queue depth |

### kiss.server

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_KISS_SERVER_ADDR` | `kiss.server.addr` | `:8100` | Non-empty TCP listen address (for example `:8100`, `127.0.0.1:8100`) | KISS TCP server listen address |
| `GOAX25_KISS_SERVER_ENABLED` | `kiss.server.enabled` | `true` | Boolean (`true`, `false`, `1`, `0`, `t`, `f`) | Enable KISS TCP server |
| `GOAX25_KISS_SERVER_MAX_CLIENTS` | `kiss.server.max_clients` | `16` | Integer `>= 0` (`0` = unlimited) | KISS TCP server max simultaneous clients (0 = unlimited) |
| `GOAX25_KISS_SERVER_PROMISCUOUS` | `kiss.server.promiscuous` | `false` | Boolean (`true`, `false`, `1`, `0`, `t`, `f`) | Register KISS TCP server client ports as promiscuous (unsupported in bridge mode) |
| `GOAX25_KISS_SERVER_READ_BUF` | `kiss.server.read_buf` | `4096` | Integer bytes (`0` uses library default; negative invalid) | KISS TCP server rx read buffer size per client (bytes) |
| `GOAX25_KISS_SERVER_TX_QUEUE_DEPTH` | `kiss.server.tx_queue_depth` | `8` | Integer (`0` uses library default; negative invalid) | KISS TCP server TX channel depth per client |

### router

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_ROUTER_MODE` | `router.mode` | `switch` | `switch`, `bridge`, `hub` | Router dispatch mode: switch, bridge, or hub |
| `GOAX25_ROUTER_PORT_QUEUE_DEPTH` | `router.port_queue_depth` | `32` | Integer (currently unused by router runtime; per-port default is fixed at 32) | Default per-port frame queue depth |

### terminal

| Environment Variable | Config Key | Default | Permissible Values | Description |
|---|---|---|---|---|
| `GOAX25_TERMINAL_CALLSIGN` | `terminal.callsign` | `N0CALL` | AX.25 address: callsign `A-Z0-9` (1-6 chars), optional `-SSID` (0-15) | Terminal local callsign |

## Notes

- Values are loaded as strings, then converted by callers (`GetInt`, `GetBool`) where needed.
- Boolean values follow Go `strconv.ParseBool` rules (for example: `true`, `false`, `1`, `0`, `t`, `f`).
