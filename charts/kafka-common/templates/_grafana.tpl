{{/*
Grafana panels laid out on the 24-column grid, as a JSON list.

Call with (dict "rows" <list>). Each row is
  (dict "title" "<row title>" "collapsed" <bool> "panels" <list>)
and each panel is a Grafana panel map plus two layout keys, `w` (default 12)
and `h` (default 8), which are removed from the output. Panels flow left to
right and wrap when the next one would pass column 24; a band is as tall as
its tallest panel.

Positions and ids are computed, never written: a board whose sections are
conditional (an SLO row only when the rules that record it exist) or repeated
(one row per connector) cannot overlap a hand-numbered `y`, which is how a
section silently ends up drawn on top of the one above it.

- A row with an empty title has no row header; its panels are placed inline.
- A collapsed row takes one grid unit; its panels are nested in the row panel,
  as Grafana stores them, with the positions they get when it is expanded.
- `id`s run from 1 in document order.
*/}}
{{- define "kafka-common.grafana.layout" -}}
{{- $y := 0 -}}
{{- $id := 0 -}}
{{- $out := list -}}
{{- range $row := (.rows | default list) -}}
{{- $collapsed := eq (include "kafka-common.enabled" (list $row.collapsed false)) "true" -}}
{{- $header := dict -}}
{{- $rowY := $y -}}
{{- if $row.title -}}
{{- $id = add1 $id -}}
{{- $header = dict "type" "row" "title" $row.title "id" $id "collapsed" $collapsed "gridPos" (dict "h" 1 "w" 24 "x" 0 "y" $y) "panels" (list) -}}
{{- $y = add1 $y -}}
{{- end -}}
{{- $x := 0 -}}
{{- $bandH := 0 -}}
{{- $children := list -}}
{{- range $p := ($row.panels | default list) -}}
{{- $w := int ($p.w | default 12) -}}
{{- $h := int ($p.h | default 8) -}}
{{- if gt (add $x $w) 24 -}}
{{- $y = add $y $bandH -}}
{{- $x = 0 -}}
{{- $bandH = 0 -}}
{{- end -}}
{{- $id = add1 $id -}}
{{- $panel := omit $p "w" "h" -}}
{{- $_ := set $panel "id" $id -}}
{{- $_ := set $panel "gridPos" (dict "h" $h "w" $w "x" $x "y" $y) -}}
{{- $children = append $children $panel -}}
{{- $x = add $x $w -}}
{{- if gt $h $bandH }}{{ $bandH = $h }}{{ end -}}
{{- end -}}
{{- $y = add $y $bandH -}}
{{- if and $row.title $collapsed -}}
{{- $_ := set $header "panels" $children -}}
{{- $out = append $out $header -}}
{{- $y = add1 $rowY -}}
{{- else -}}
{{- if $row.title }}{{ $out = append $out $header }}{{ end -}}
{{- $out = concat $out $children -}}
{{- end -}}
{{- end -}}
{{- toJson $out -}}
{{- end }}
