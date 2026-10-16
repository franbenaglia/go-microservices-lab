# Imprime el árbol de spans de una traza devuelta por la API v3 de Jaeger (OTLP JSON).
[ .result.resourceSpans[] as $rs
  | ($rs.resource.attributes[] | select(.key == "service.name") | .value.stringValue) as $svc
  | $rs.scopeSpans[].spans[]
  | {svc: $svc, id: .spanId, parent: (.parentSpanId // ""), name: .name,
     start: (.startTimeUnixNano | tonumber), ms: (((.endTimeUnixNano | tonumber) - (.startTimeUnixNano | tonumber)) / 1e6),
     err: (.status.code == 2 or .status.code == "STATUS_CODE_ERROR")} ] as $spans
| ($spans | map({key: .id, value: .}) | from_entries) as $by
| def depth($s): if ($s.parent == "" or $by[$s.parent] == null) then 0 else 1 + depth($by[$s.parent]) end;
  def walk($id): ($spans | map(select(.parent == $id)) | sort_by(.start)[]) as $c
    | "\("  " * depth($c))\($c.name)  [\($c.svc)]  \($c.ms * 10 | floor / 10)ms\(if $c.err then "  ERROR" else "" end)", walk($c.id);
  ($spans | map(select(.parent == "" or $by[.parent] == null)) | sort_by(.start)[]) as $root
  | "\($root.name)  [\($root.svc)]  \($root.ms * 10 | floor / 10)ms", walk($root.id)
