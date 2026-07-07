package otelfile

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"text/template" // Swapped from html/template to prevent HTML escaping of JSON quotes

	"go.opentelemetry.io/otel/sdk/trace"
)

type SerializableSpan struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id"`
	Name         string            `json:"name"`
	Service      string            `json:"service"`
	StartTimeMs  int64             `json:"start_time_ms"`
	EndTimeMs    int64             `json:"end_time_ms"`
	Attributes   map[string]string `json:"attributes"`
}

type FileExporter struct {
	mu         sync.Mutex
	filePath   string
	maxTraces  int                           // Rolling limit
	traceOrder []string                      // Track TraceID order for eviction
	traces     map[string][]SerializableSpan // Grouped spans by TraceID
}

// NewFileExporter creates a new exporter that maintains a rolling log of traces in a single HTML file.
func NewFileExporter(filePath string, maxTraces int) *FileExporter {
	if maxTraces <= 0 {
		maxTraces = 1000 // Default limit
	}
	return &FileExporter{
		filePath:   filePath,
		maxTraces:  maxTraces,
		traceOrder: make([]string, 0),
		traces:     make(map[string][]SerializableSpan),
	}
}

func (e *FileExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, s := range spans {
		traceID := s.SpanContext().TraceID().String()
		spanID := s.SpanContext().SpanID().String()

		parentID := ""
		if s.Parent().IsValid() {
			parentID = s.Parent().SpanID().String()
		}

		attrs := make(map[string]string)
		serviceName := "unknown-service"
		for _, kv := range s.Resource().Attributes() {
			if kv.Key == "service.name" {
				serviceName = kv.Value.AsString()
			}
		}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		attrs["service.name"] = serviceName

		serializable := SerializableSpan{
			TraceID:      traceID,
			SpanID:       spanID,
			ParentSpanID: parentID,
			Name:         s.Name(),
			Service:      serviceName,
			StartTimeMs:  s.StartTime().UnixNano() / 1e6,
			EndTimeMs:    s.EndTime().UnixNano() / 1e6,
			Attributes:   attrs,
		}

		// Track unique traces and maintain order for eviction
		if _, exists := e.traces[traceID]; !exists {
			if len(e.traceOrder) >= e.maxTraces {
				// Evict oldest trace
				oldest := e.traceOrder[0]
				e.traceOrder = e.traceOrder[1:]
				delete(e.traces, oldest)
			}
			e.traceOrder = append(e.traceOrder, traceID)
		}

		e.traces[traceID] = append(e.traces[traceID], serializable)
	}

	return e.writeHTML()
}

func (e *FileExporter) Shutdown(ctx context.Context) error {
	return nil
}

func (e *FileExporter) writeHTML() error {
	// Flatten grouped traces back into a single slice for serialization
	allSpans := make([]SerializableSpan, 0)
	// We iterate in order so the oldest traces are serialized first
	for _, id := range e.traceOrder {
		allSpans = append(allSpans, e.traces[id]...)
	}

	jsonData, err := json.Marshal(allSpans)
	if err != nil {
		return err
	}

	tmpl, err := template.New("html").Parse(htmlTemplate)
	if err != nil {
		return err
	}

	file, err := os.Create(e.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return tmpl.Execute(file, map[string]interface{}{
		"SpansJSON": string(jsonData),
	})
}

// Embedded clean UI featuring a trace sidebar, waterfall chart, and details panel
const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>OTel File Trace Explorer</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            margin: 0;
            background-color: #f8f9fa;
            color: #212529;
            display: flex;
            flex-direction: column;
            height: 100vh;
        }
        header {
            background-color: #1e293b;
            color: white;
            padding: 12px 20px;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .main-container {
            display: flex;
            flex: 1;
            overflow: hidden;
        }
        .sidebar {
            width: 300px;
            background-color: #ffffff;
            border-right: 1px solid #e2e8f0;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
        }
        .sidebar-header {
            padding: 12px;
            font-weight: bold;
            background-color: #f1f5f9;
            border-bottom: 1px solid #e2e8f0;
            font-size: 13px;
        }
        .trace-item {
            padding: 12px;
            border-bottom: 1px solid #f1f5f9;
            cursor: pointer;
            transition: background-color 0.1s;
        }
        .trace-item:hover {
            background-color: #f8fafc;
        }
        .trace-item.active {
            background-color: #e2e8f0;
            border-left: 4px solid #3b82f6;
        }
        .trace-name {
            font-weight: 600;
            font-size: 13px;
            margin-bottom: 4px;
            word-break: break-all;
        }
        .trace-meta {
            font-size: 11px;
            color: #64748b;
        }
        .workspace {
            display: flex;
            flex: 1;
            overflow: hidden;
        }
        .timeline-container {
            flex: 1;
            padding: 20px;
            overflow-y: auto;
            border-right: 1px solid #e2e8f0;
        }
        .details-panel {
            width: 380px;
            padding: 20px;
            background-color: #ffffff;
            overflow-y: auto;
        }
        .span-row {
            display: flex;
            align-items: center;
            margin-bottom: 6px;
            cursor: pointer;
            padding: 4px;
            border-radius: 4px;
        }
        .span-row:hover {
            background-color: #f1f5f9;
        }
        .span-name {
            width: 250px;
            font-weight: 500;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
            font-size: 12px;
        }
        .span-bar-container {
            flex: 1;
            background-color: #f1f5f9;
            position: relative;
            height: 20px;
            border-radius: 4px;
        }
        .span-bar {
            position: absolute;
            height: 100%;
            border-radius: 3px;
            display: flex;
            align-items: center;
            padding-left: 6px;
            color: white;
            font-size: 10px;
            font-weight: bold;
            box-sizing: border-box;
        }
        .details-title {
            margin-top: 0;
            border-bottom: 2px solid #1e293b;
            padding-bottom: 8px;
            font-size: 15px;
        }
        .attr-table {
            width: 100%;
            border-collapse: collapse;
            font-size: 12px;
            margin-top: 10px;
        }
        .attr-table th, .attr-table td {
            text-align: left;
            padding: 6px;
            border-bottom: 1px solid #e2e8f0;
            word-break: break-all;
        }
        .attr-table th {
            background-color: #f8fafc;
            width: 35%;
            color: #475569;
        }
        .empty-state {
            display: flex;
            align-items: center;
            justify-content: center;
            height: 100%;
            color: #94a3b8;
            font-size: 14px;
        }
    </style>
</head>
<body>
    <header>
        <strong style="font-size: 15px;"> <svg
          xmlns="http://www.w3.org/2000/svg"
          viewBox="0 0 512 512"
          width="40"
          height="40"
        >
          <rect width="512" height="512" rx="120" fill="#0f172a" />
          <path
            d="M160 100h120l80 80v216a16 16 0 0 1-16 16H160a16 16 0 0 1-16-16V116a16 16 0 0 1 16-16z"
            fill="#f8fafc"
          />
          <path d="M280 100v64a16 16 0 0 0 16 16h64z" fill="#cbd5e1" />
          <rect x="180" y="215" width="130" height="24" rx="6" fill="#3b82f6" />
          <rect x="210" y="255" width="75" height="24" rx="6" fill="#10b981" />
          <rect x="260" y="295" width="55" height="24" rx="6" fill="#f59e0b" />
          <rect x="290" y="335" width="35" height="24" rx="6" fill="#8b5cf6" />
        </svg> OtelFile</strong> 
        <span id="global-stats" style="font-size: 12px;"></span>
    </header>
    <div class="main-container">
        <div class="sidebar" id="sidebar">
            <div class="sidebar-header">Recent Traces</div>
            <div id="trace-list"></div>
        </div>
        <div class="workspace">
            <div class="timeline-container" id="timeline">
                <div class="empty-state">Select a trace from the left panel to begin analysis</div>
            </div>
            <div class="details-panel" id="details">
                <h3 class="details-title">Details</h3>
                <p style="font-size: 12px; color: #64748b;">Select an individual span inside the timeline to view attributes.</p>
            </div>
        </div>
    </div>

    <script id="trace-raw-data" type="application/json">
    {{.SpansJSON}}
    </script>

    <script>
        const colors = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#06b6d4'];
        function getColor(str) {
            let hash = 0;
            for (let i = 0; i < str.length; i++) {
                hash = str.charCodeAt(i) + ((hash << 5) - hash);
            }
            return colors[Math.abs(hash) % colors.length];
        }

        const rawData = JSON.parse(document.getElementById('trace-raw-data').textContent || '[]');
        
        // Group spans by TraceID
        const traces = {};
        rawData.forEach(span => {
            if (!traces[span.trace_id]) {
                traces[span.trace_id] = [];
            }
            traces[span.trace_id].push(span);
        });

        // Collect stats
        const traceIds = Object.keys(traces);
        document.getElementById('global-stats').textContent = "Total traces: " + traceIds.length;

        const traceListContainer = document.getElementById('trace-list');
        
        // Populate Sidebar
        traceIds.reverse().forEach((traceId, idx) => {
            const spans = traces[traceId];
            
            // Try to find the root span (no parent_span_id), otherwise use the first span
            const rootSpan = spans.find(s => !s.parent_span_id) || spans[0];
            const minTime = Math.min(...spans.map(s => s.start_time_ms));
            const maxTime = Math.max(...spans.map(s => s.end_time_ms));
            const duration = maxTime - minTime;

            const item = document.createElement('div');
            item.className = 'trace-item';
            if (idx === 0) item.classList.add('active');
            item.innerHTML = 
                '<div class="trace-name">' + rootSpan.name + '</div>' +
                '<div class="trace-meta">' +
                    'Duration: ' + duration + 'ms | Spans: ' + spans.length + '<br>' +
                    'Service: ' + rootSpan.service +
                '</div>';

            item.addEventListener('click', () => {
                document.querySelectorAll('.trace-item').forEach(el => el.classList.remove('active'));
                item.classList.add('active');
                renderTrace(traceId);
            });

            traceListContainer.appendChild(item);
        });

        // Render Timeline
        function renderTrace(traceId) {
            const spans = traces[traceId];
            const timeline = document.getElementById('timeline');
            timeline.innerHTML = '';

            const minTime = Math.min(...spans.map(s => s.start_time_ms));
            const maxTime = Math.max(...spans.map(s => s.end_time_ms));
            const totalDuration = maxTime - minTime || 1;

            // Build hierarchy
            const spansById = {};
            spans.forEach(s => { s.children = []; spansById[s.span_id] = s; });

            const rootSpans = [];
            spans.forEach(s => {
                if (s.parent_span_id && spansById[s.parent_span_id]) {
                    spansById[s.parent_span_id].children.push(s);
                } else {
                    rootSpans.push(s);
                }
            });

            const orderedSpans = [];
            function traverse(span, depth = 0) {
                span.depth = depth;
                orderedSpans.push(span);
                span.children.sort((a,b) => a.start_time_ms - b.start_time_ms);
                span.children.forEach(c => traverse(c, depth + 1));
            }
            rootSpans.sort((a,b) => a.start_time_ms - b.start_time_ms).forEach(r => traverse(r, 0));

            // Render Rows
            orderedSpans.forEach(span => {
                const row = document.createElement('div');
                row.className = 'span-row';
                
                const leftPct = ((span.start_time_ms - minTime) / totalDuration) * 100;
                const widthPct = Math.max(((span.end_time_ms - span.start_time_ms) / totalDuration) * 100, 0.5);
                const duration = span.end_time_ms - span.start_time_ms;
                
                const indent = span.depth * 14;
                const barColor = getColor(span.service);

                row.innerHTML = 
                    '<div class="span-name" style="padding-left: ' + indent + 'px;">' +
                        (span.depth > 0 ? '└─ ' : '') + span.name +
                    '</div>' +
                    '<div class="span-bar-container">' +
                        '<div class="span-bar" style="left: ' + leftPct + '%; width: ' + widthPct + '%; background-color: ' + barColor + ';">' +
                            duration + 'ms' +
                        '</div>' +
                    '</div>';

                row.addEventListener('click', () => showDetails(span, minTime));
                timeline.appendChild(row);
            });
        }

        function showDetails(span, minTime) {
            const details = document.getElementById('details');
            let attrRows = '';
            for (const [key, value] of Object.entries(span.attributes)) {
                attrRows += '<tr><th>' + key + '</th><td>' + value + '</td></tr>';
            }

            details.innerHTML = 
                '<h3 class="details-title">' + span.name + '</h3>' +
                '<table class="attr-table">' +
                    '<tr><th>Service</th><td>' + span.service + '</td></tr>' +
                    '<tr><th>Span ID</th><td>' + span.span_id + '</td></tr>' +
                    '<tr><th>Parent ID</th><td>' + (span.parent_span_id || 'None') + '</td></tr>' +
                    '<tr><th>Start Offset</th><td>' + (span.start_time_ms - minTime) + 'ms</td></tr>' +
                    '<tr><th>Duration</th><td>' + (span.end_time_ms - span.start_time_ms) + 'ms</td></tr>' +
                    attrRows +
                '</table>';
        }

        // Render initial trace
        if (traceIds.length > 0) {
            renderTrace(traceIds[traceIds.length - 1]);
        }

        // Pure JS auto-refresh: polls the page itself to detect writes/updates
        let lastContentLength = null;
        setInterval(function() {
            fetch(window.location.href, { cache: 'no-store' })
                .then(function(res) { 
                    return res.text(); 
                })
                .then(function(text) {
                    if (lastContentLength === null) {
                        lastContentLength = text.length;
                    } else if (text.length !== lastContentLength) {
                        // The file has been updated with new traces on disk. Reload.
                        window.location.reload();
                    }
                })
                .catch(function(err) {
                    // Handles browser file:// sandbox limitations gracefully if opened offline.
                });
        }, 1000); // Checks every 1 second
    </script>
</body>
</html>`
