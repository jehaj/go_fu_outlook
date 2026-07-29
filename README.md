# Local Graph-to-IMAP/SMTP Mail Proxy (`graph-mail-proxy`)

A lightweight, single-user background proxy for Linux that allows standard IMAP/SMTP mail clients (such as **Mozilla Thunderbird**) to read and send email through Microsoft 365 / Azure AD work or school mailboxes via the **Microsoft Graph API**, instead of Exchange/EWS protocols.

---

## 1. Security Architecture & Policies

> [!IMPORTANT]
> **Loopback Binding Only:** IMAP (`127.0.0.1:1143`) and SMTP (`127.0.0.1:1025`) servers bind exclusively to the loopback interface (`127.0.0.1`). The proxy strictly refuses to start if configured to listen on external or non-loopback network interfaces (`0.0.0.0`, LAN IPs).

> [!WARNING]
> **Plaintext-over-Loopback (v1 Transport):** Transport in v1 is **plaintext over loopback (`127.0.0.1`) by design**. Connections never leave the local machine, limiting risk to other local OS users on the same host.
> - **Phase 2 TLS Roadmap:** TLS support (STARTTLS / implicit TLS) with locally generated self-signed certificates is planned as a Phase 2 follow-up feature.

> [!CAUTION]
> **Azure AD Auth & Tenant Policy:**
> - If self-service app registration is restricted in your tenant, the proxy authenticates using pre-consented **Microsoft first-party public Client IDs** (e.g., Microsoft Office `d3590ed6-52b3-4102-aeff-aad2292ab01c`, Teams `1fec8e78-bce4-4aaf-ab1b-5451cc387264`, Azure CLI `04b07795-8ddb-461a-bbee-02f9e1bf7b46`, Azure PowerShell `1950a258-227b-4e31-a9cf-717495945fc2`).
> - **Token Storage:** Refresh and access tokens are stored in `~/.config/graph-mail-proxy/tokens.json` with strict file permissions (`0600`) and directory permissions (`0700`).
> - **Log Redaction:** All sensitive credentials (tokens, Authorization headers, local passwords) are automatically redacted in log output (`[REDACTED]`).

---

## 2. Quick Start & Installation

### Step 1: Build Binaries
```bash
go build -o ~/.local/bin/graph-mail-proxy ./cmd/graph-mail-proxy
go build -o ~/.local/bin/auth-spike ./cmd/auth-spike
```

### Step 2: Configure Proxy
Create the configuration directory and file `~/.config/graph-mail-proxy/config.yaml`:
```bash
mkdir -p ~/.config/graph-mail-proxy
cp config.example.yaml ~/.config/graph-mail-proxy/config.yaml
```

### Step 3: Interactive OAuth2 Device-Code Authentication
Test authentication against your Azure AD tenant using `auth-spike` or `graph-mail-proxy -auth-only`:
```bash
graph-mail-proxy -auth-only
```
Follow the on-screen prompt: open `https://microsoft.com/devicelogin` in your browser and enter the displayed code.

### Step 4: Systemd User Unit Deployment
Install and start the background service for autostart on login and automatic restart on failure:
```bash
mkdir -p ~/.config/systemd/user/
cp systemd/graph-mail-proxy.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now graph-mail-proxy
```
Check service status:
```bash
systemctl --user status graph-mail-proxy
```

---

## 3. Mozilla Thunderbird Setup Guide

1. Open Thunderbird -> **Account Settings** -> **Account Actions** -> **Add Mail Account**.
2. Enter your Name and Email Address. Deselect "Automatic configuration" and click **Configure manually**.

### IMAP (Incoming Server)
- **Protocol:** IMAP
- **Server Hostname:** `127.0.0.1`
- **Port:** `1143`
- **Connection Security:** None
- **Authentication Method:** Normal password
- **Username:** `thunderbird` (or matching `local_auth.username` in `config.yaml`)
- **Password:** `localpassword` (or matching `local_auth.password` in `config.yaml`)

### SMTP (Outgoing Server)
- **Server Hostname:** `127.0.0.1`
- **Port:** `1025`
- **Connection Security:** None
- **Authentication Method:** Normal password
- **Username:** `thunderbird`
- **Password:** `localpassword`

---

## 4. Verification & Testing

Run all automated unit and integration tests:
```bash
go test -v ./...
```
Tests cover config loopback validation, log redaction, token file `0600` permissions, SQLite UID mapping, IMAP & SMTP protocol handlers, and end-to-end integration against a mock Graph API server.
