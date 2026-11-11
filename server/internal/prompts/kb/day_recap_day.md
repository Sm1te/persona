{{with .Pinned}}已确认事实（只读，以此为准）：{{.}}
{{end}}全天录音：{{if .Title}}「{{.Title}}」，{{end}}{{.Day}}（{{.Weekday}}），时区 {{.Zone}}{{with .Part}}，第 {{.}} 部分{{end}}{{with .Language}}，语言 {{.}}{{end}}
