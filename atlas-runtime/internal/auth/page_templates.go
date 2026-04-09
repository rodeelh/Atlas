package auth

import "fmt"

func AuthCardPage(title, body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Atlas — %s</title>
<style>
:root{
  --bg:#111111;
  --surface:#1a1a1a;
  --border:#333333;
  --text:#f5f5f5;
  --text-2:#cfcfcf;
}
*{box-sizing:border-box}
html,body{min-height:100%%}
body{
  margin:0;
  padding:20px;
  font-family:-apple-system,system-ui,sans-serif;
  background:var(--bg);
  color:var(--text);
  display:flex;
  align-items:center;
  justify-content:center;
  min-height:100vh;
}
.card{
  max-width:520px;
  width:100%%;
  background:var(--surface);
  border:1px solid var(--border);
  border-radius:12px;
  padding:24px;
  line-height:1.5;
}
h1{
  margin:0 0 8px;
  font-size:20px;
}
p{
  margin:0 0 10px;
  color:var(--text-2);
}
code{
  background:#0f0f0f;
  border:1px solid var(--border);
  padding:2px 6px;
  border-radius:6px;
}
strong{
  color:var(--text);
}
</style>
</head>
<body>
<div class="card">
  <h1>%s</h1>
  %s
</div>
</body>
</html>`, title, title, body)
}

func LanDisabledHTML() string {
	return AuthCardPage(
		"Remote Access Disabled",
		`<p>This Atlas runtime is not currently accepting remote connections.</p>
<p>To enable access, open <strong>Atlas</strong> on the host Mac and go to <strong>Settings &rarr; Remote Access</strong>, then turn on <strong>LAN Access</strong>.</p>`,
	)
}

func TailscaleDisabledHTML() string {
	return AuthCardPage(
		"Tailscale Access Is Disabled",
		`<p>This request came from a Tailscale device, but Tailscale access is not currently enabled for this Atlas runtime.</p>
<p>Open <strong>Atlas</strong> on the host Mac, go to <strong>Settings &rarr; Remote Access</strong>, and turn on <strong>Tailscale Access</strong> if you want devices on your Tailnet to connect directly.</p>
<p>LAN access is still available through the configured remote access flow.</p>`,
	)
}

func HTTPSRequiredHTML() string {
	return AuthCardPage(
		"HTTPS Required For Remote LAN Access",
		`<p>Atlas now blocks remote LAN authentication over plain HTTP to protect access keys and sessions from interception.</p>
<p>Use a local HTTPS reverse proxy (for example Caddy or Nginx) in front of Atlas and forward to <code>http://127.0.0.1:1984</code>.</p>
<p>Tailscale access remains available without this requirement because transport encryption is provided by Tailscale.</p>`,
	)
}
