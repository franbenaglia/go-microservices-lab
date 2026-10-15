# Agente Consul del laboratorio: un único servidor (sin alta disponibilidad).
# Se ejecuta en modo servidor con data_dir persistente, así que el catálogo y el
# KV sobreviven a los reinicios (al contrario que en el modo -dev).
datacenter       = "lab"
data_dir         = "/consul/data"
server           = true
bootstrap_expect = 1
node_name        = "consul-1"

# La API y la UI escuchan en todas las interfaces del contenedor; en el host
# solo se publican en 127.0.0.1 (ver compose.yaml).
client_addr = "0.0.0.0"
bind_addr   = "{{ GetPrivateIP }}"

ui_config {
  enabled = true
}

# Laboratorio local: sin ACLs ni TLS. Las comprobaciones de tipo script están
# desactivadas (solo HTTP, TCP y TTL).
enable_script_checks = false
log_level            = "INFO"
