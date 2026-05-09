CREATE TABLE IF NOT EXISTS hosts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ip_address     TEXT    NOT NULL UNIQUE,
    mac_address    TEXT    NOT NULL DEFAULT '',
    hostname       TEXT    NOT NULL DEFAULT '',
    os_fingerprint TEXT    NOT NULL DEFAULT '',
    vendor         TEXT    NOT NULL DEFAULT '',
    device_type    TEXT    NOT NULL DEFAULT '',
    first_seen     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ports (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    host_id    INTEGER  NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    port       INTEGER  NOT NULL,
    protocol   TEXT     NOT NULL DEFAULT 'tcp',
    service    TEXT     NOT NULL DEFAULT '',
    state      TEXT     NOT NULL DEFAULT 'open',
    first_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(host_id, port, protocol)
);

CREATE TABLE IF NOT EXISTS scans (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    subnet      TEXT     NOT NULL,
    hosts_found INTEGER  NOT NULL DEFAULT 0,
    started_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);
