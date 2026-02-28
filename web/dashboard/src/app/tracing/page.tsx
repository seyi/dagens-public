import React from "react";
import { Activity, ExternalLink, ShieldCheck } from "lucide-react";

export default function TracingPage() {
  return (
    <div className="flex flex-col items-center justify-center min-h-[70vh] space-y-8 max-w-2xl mx-auto text-center">
      <div className="p-4 rounded-full bg-blue-500/10 border border-blue-500/20">
        <Activity className="h-16 w-16 text-blue-500" />
      </div>
      
      <div>
        <h1 className="text-4xl font-bold tracking-tight mb-4">Distributed Tracing</h1>
        <p className="text-lg text-gray-400">
          Dagens is integrated with OpenTelemetry and Jaeger. Every agent decision, 
          gRPC call, and scheduler event is tracked with sub-millisecond precision.
        </p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 w-full">
        <div className="p-6 bg-gray-900 border border-gray-800 rounded-xl text-left">
          <ShieldCheck className="h-6 w-6 text-green-500 mb-2" />
          <h3 className="font-semibold mb-1">Audit Trail</h3>
          <p className="text-sm text-gray-400">Immutable record of every LLM interaction and worker dispatch.</p>
        </div>
        <div className="p-6 bg-gray-900 border border-gray-800 rounded-xl text-left">
          <Activity className="h-6 w-6 text-purple-500 mb-2" />
          <h3 className="font-semibold mb-1">Performance</h3>
          <p className="text-sm text-gray-400">Visualize bottlenecks in parallel agent execution stages.</p>
        </div>
      </div>

      <a 
        href="http://localhost:16686" 
        target="_blank" 
        rel="noopener noreferrer"
        className="inline-flex items-center px-8 py-4 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold text-lg transition-all shadow-lg shadow-blue-500/20 group"
      >
        Open Jaeger UI
        <ExternalLink className="ml-3 h-5 w-5 group-hover:translate-x-1 group-hover:-translate-y-1 transition-transform" />
      </a>
      
      <p className="text-xs text-gray-500 font-mono">
        OTEL_EXPORTER_OTLP_ENDPOINT: localhost:4317
      </p>
    </div>
  );
}
