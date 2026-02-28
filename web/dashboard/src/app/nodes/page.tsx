import React from "react";
import { Server, Cpu, Globe, CheckCircle } from "lucide-react";

const nodes = [
  { id: "worker-1", type: "Go Runtime", address: "172.18.0.3", port: 50051, status: "Ready", cpu: "12%", mem: "256MB" },
  { id: "worker-2", type: "Go Runtime", address: "172.18.0.4", port: 50051, status: "Ready", cpu: "8%", mem: "240MB" },
];

export default function NodesPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Cluster Nodes</h1>
        <p className="text-gray-400 mt-1">Status and resource usage of your distributed workers.</p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {nodes.map((node) => (
          <div key={node.id} className="bg-gray-900 border border-gray-800 rounded-xl p-6 relative overflow-hidden group hover:border-blue-500/50 transition-all">
            <div className="absolute top-0 right-0 p-4 opacity-10 group-hover:opacity-20 transition-opacity">
              <Server className="h-24 w-24" />
            </div>
            
            <div className="flex items-center justify-between mb-6">
              <div className="flex items-center">
                <div className="p-2 rounded bg-blue-600/20 mr-3">
                  <Cpu className="h-5 w-5 text-blue-500" />
                </div>
                <h3 className="text-lg font-semibold">{node.id}</h3>
              </div>
              <span className="px-2 py-1 text-[10px] font-bold bg-green-900/30 text-green-400 border border-green-800/50 rounded-full uppercase">
                {node.status}
              </span>
            </div>

            <div className="space-y-3">
              <div className="flex items-center text-sm text-gray-400">
                <Globe className="h-4 w-4 mr-2" />
                {node.address}:{node.port}
              </div>
              <div className="grid grid-cols-2 gap-4 pt-4 border-t border-gray-800">
                <div>
                  <p className="text-[10px] text-gray-500 uppercase font-bold tracking-wider">CPU Usage</p>
                  <p className="text-lg font-mono text-white">{node.cpu}</p>
                </div>
                <div>
                  <p className="text-[10px] text-gray-500 uppercase font-bold tracking-wider">Memory</p>
                  <p className="text-lg font-mono text-white">{node.mem}</p>
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
