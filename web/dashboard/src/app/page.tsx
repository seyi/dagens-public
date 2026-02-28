import React from "react";
import { cn } from "@/lib/utils";
import { 
  Server, 
  Cpu, 
  CheckCircle, 
  Clock,
  Play
} from "lucide-react";

const stats = [
  { name: "Active Nodes", value: "2", icon: Server, color: "text-blue-500" },
  { name: "Running Jobs", value: "0", icon: Play, color: "text-green-500" },
  { name: "Completed Tasks", value: "124", icon: CheckCircle, color: "text-purple-500" },
  { name: "Avg. Latency", value: "42ms", icon: Clock, color: "text-orange-500" },
];

export default function DashboardPage() {
  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-3xl font-bold">Mission Control</h1>
        <p className="text-gray-400 mt-2">Real-time status of your distributed AI agent cluster.</p>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {stats.map((item) => (
          <div key={item.name} className="bg-gray-900 border border-gray-800 rounded-xl p-6 hover:border-gray-700 transition-all shadow-sm">
            <div className="flex items-center">
              <div className={cn("p-2 rounded-lg bg-gray-800", item.color)}>
                <item.icon className="h-6 w-6" />
              </div>
              <div className="ml-4">
                <p className="text-sm font-medium text-gray-400 truncate">{item.name}</p>
                <p className="mt-1 text-2xl font-semibold text-white">{item.value}</p>
              </div>
            </div>
          </div>
        ))}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {/* Recent Activity */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-6">
          <h2 className="text-xl font-semibold mb-4 flex items-center">
            <Clock className="h-5 w-5 mr-2 text-blue-500" />
            Recent Activity
          </h2>
          <div className="space-y-4">
            <div className="border-l-2 border-green-500 pl-4 py-1">
              <p className="text-sm font-medium">Job: market-research-v4</p>
              <p className="text-xs text-gray-500">Completed 2 minutes ago on worker-1</p>
            </div>
            <div className="border-l-2 border-green-500 pl-4 py-1">
              <p className="text-sm font-medium">Job: haiku-generator</p>
              <p className="text-xs text-gray-500">Completed 5 minutes ago on worker-2</p>
            </div>
          </div>
        </div>

        {/* Cluster Health */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-6">
          <h2 className="text-xl font-semibold mb-4 flex items-center">
            <Cpu className="h-5 w-5 mr-2 text-purple-500" />
            Cluster Health
          </h2>
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <span className="text-sm">worker-1</span>
              <span className="px-2 py-1 text-xs bg-green-900 text-green-300 rounded">Healthy</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm">worker-2</span>
              <span className="px-2 py-1 text-xs bg-green-900 text-green-300 rounded">Healthy</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}