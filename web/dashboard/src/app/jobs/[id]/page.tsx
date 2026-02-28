"use client";

import React, { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { ArrowLeft, Clock, CheckCircle, AlertCircle, Loader2, ShieldAlert } from "lucide-react";
import { JobGraph } from "@/components/graph/JobGraph";
import { cn } from "@/lib/utils";

const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export default function JobDetailsPage() {
  const params = useParams();
  const id = params?.id as string;

  const [job, setJob] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;

    const fetchJob = async () => {
      try {
        // Use the Next.js proxy to avoid CORS/Network issues
        const res = await fetch(`/api/v1/jobs/${id}`);
        if (!res.ok) {
            const text = await res.text();
            console.error(`API Error: ${res.status} ${res.statusText}`, text);
            throw new Error(`Failed to fetch job: ${res.status} ${res.statusText}`);
        }
        const data = await res.json();
        setJob(data);
      } catch (err) {
        console.error(err);
        setError("Could not load job details. Is the API server running?");
      } finally {
        setLoading(false);
      }
    };

    fetchJob();
    // Poll every 2 seconds for live updates
    const interval = setInterval(fetchJob, 2000);
    return () => clearInterval(interval);
  }, [id]);

  if (loading && !job) {
    return (
      <div className="flex items-center justify-center h-96">
        <Loader2 className="h-8 w-8 text-blue-500 animate-spin" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-6 bg-red-900/20 border border-red-900/50 rounded-xl text-red-200">
        <div className="flex items-center mb-2">
          <AlertCircle className="h-5 w-5 mr-2" />
          <h2 className="font-semibold">Error</h2>
        </div>
        <p>{error}</p>
        <Link href="/jobs" className="mt-4 inline-block text-sm underline hover:text-white">
          &larr; Back to Jobs
        </Link>
      </div>
    );
  }

  // Transform Job stages/tasks back into a graph structure for visualization
  const graphData = {
    nodes: job?.Stages?.flatMap((stage: any) => 
      stage.Tasks.map((task: any) => ({
        id: task.AgentID,
        // Use AgentName (e.g. "poet") instead of generic ID if available
        name: task.AgentName !== "function" ? task.AgentName : task.AgentID,
        type: "agent",
        status: task.Status 
      }))
    ) || [],
    edges: job?.Edges || [] 
  };
  
  return (
    <div className="space-y-6">
      <div className="flex items-center space-x-4">
        <Link href="/jobs" className="p-2 hover:bg-gray-800 rounded-lg transition-colors">
          <ArrowLeft className="h-5 w-5 text-gray-400" />
        </Link>
        <div>
          <h1 className="text-2xl font-bold tracking-tight flex items-center">
            {job?.Name || "Job Details"}
            <span className={cn(
              "ml-3 px-2 py-0.5 text-xs font-bold rounded-full border uppercase tracking-wider",
              job?.Status === "COMPLETED" ? "bg-green-900/30 text-green-400 border-green-800/50" :
              job?.Status === "RUNNING" ? "bg-blue-900/30 text-blue-400 border-blue-800/50 animate-pulse" :
              job?.Status === "BLOCKED" ? "bg-amber-900/30 text-amber-400 border-amber-800/50" :
              "bg-red-900/30 text-red-400 border-red-800/50"
            )}>
              {job?.Status === "BLOCKED" && <ShieldAlert className="inline h-3 w-3 mr-1" />}
              {job?.Status}
            </span>
          </h1>
          <p className="text-sm text-gray-500 font-mono mt-1">{id}</p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Graph View */}
        <div className="lg:col-span-2 space-y-4">
            <h2 className="text-lg font-semibold flex items-center">
                <div className="h-2 w-2 rounded-full bg-blue-500 mr-2" />
                Live Execution Graph
            </h2>
            <JobGraph data={graphData.nodes.length > 0 ? graphData : mockData} />
        </div>

        {/* Details Panel */}
        <div className="space-y-6">
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-6">
            <h3 className="text-sm font-semibold text-gray-400 uppercase tracking-wider mb-4">Metadata</h3>
            <div className="space-y-4">
                <div>
                    <p className="text-xs text-gray-500">Created At</p>
                    <p className="text-sm flex items-center mt-1">
                        <Clock className="h-3 w-3 mr-1.5 text-gray-600" />
                        {new Date(job?.CreatedAt).toLocaleString()}
                    </p>
                </div>
                <div>
                    <p className="text-xs text-gray-500">Stages</p>
                    <p className="text-sm mt-1">{job?.Stages?.length || 0}</p>
                </div>
            </div>
          </div>

          <div className="bg-gray-900 border border-gray-800 rounded-xl p-6 flex flex-col h-[400px]">
            <h3 className="text-sm font-semibold text-gray-400 uppercase tracking-wider mb-4">Task Logs</h3>
            <div className="flex-1 overflow-y-auto space-y-4 pr-2">
                {job?.Stages?.map((stage: any) => (
                    stage.Tasks.map((task: any) => (
                        <div key={task.ID} className="group text-sm border-l-2 border-gray-700 pl-4 py-2 hover:border-blue-500 transition-colors">
                            <div className="flex justify-between items-center mb-2">
                                <span className="font-mono font-medium text-blue-400">{task.AgentName}</span>
                                <span className={cn(
                                    "text-[10px] uppercase font-bold flex items-center",
                                    task.Status === "COMPLETED" ? "text-green-500" : 
                                    task.Status === "RUNNING" ? "text-blue-500 animate-pulse" : 
                                    task.Status === "BLOCKED" ? "text-amber-500" : "text-red-500"
                                )}>
                                    {task.Status === "BLOCKED" && <ShieldAlert className="h-3 w-3 mr-1" />}
                                    {task.Status}
                                </span>
                            </div>
                            {task.Output?.Result ? (
                                <pre className="text-gray-400 text-xs whitespace-pre-wrap font-mono bg-gray-950/50 p-3 rounded-lg border border-gray-800 overflow-x-auto">
                                    {task.Output.Result}
                                </pre>
                            ) : (
                                <p className="text-gray-600 text-xs italic">No output available.</p>
                            )}
                        </div>
                    ))
                ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// Fallback visual data if the API response structure is flat/empty
const mockData = {
  nodes: [
    { id: "planner", type: "agent", name: "Planner" },
    { id: "worker", type: "agent", name: "Worker" },
  ],
  edges: [
    { from: "planner", to: "worker" }
  ]
};
