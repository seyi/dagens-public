"use client";

import React, { useState } from "react";
import Link from "next/link";
import { 
  Plus, 
  Search, 
  Filter, 
  MoreVertical,
  Play,
  Terminal,
  ExternalLink,
  Activity,
  ShieldAlert
} from "lucide-react";
import { cn } from "@/lib/utils";

export default function JobsPage() {
  const [search, setSearch] = useState("");
  const [jobs, setJobs] = useState<any[]>([]);

  React.useEffect(() => {
    fetch("/api/v1/jobs")
      .then((res) => res.json())
      .then((data) => {
          // Normalize data structure if needed
          setJobs(Array.isArray(data) ? data : []);
      })
      .catch(console.error);
  }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Agent Jobs</h1>
          <p className="text-gray-400 mt-1">Manage and monitor distributed agent workflows.</p>
        </div>
        <button className="inline-flex items-center px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors">
          <Plus className="h-5 w-5 mr-2" />
          Submit Job
        </button>
      </div>

      {/* Filters */}
      <div className="flex items-center space-x-4 bg-gray-900/50 p-4 rounded-xl border border-gray-800">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-500" />
          <input 
            type="text" 
            placeholder="Search jobs..." 
            className="w-full bg-black border border-gray-800 rounded-lg py-2 pl-10 pr-4 focus:ring-1 focus:ring-blue-500 outline-none text-sm transition-all"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <button className="inline-flex items-center px-3 py-2 border border-gray-800 rounded-lg text-sm hover:bg-gray-800 transition-colors">
          <Filter className="h-4 w-4 mr-2" />
          Filter
        </button>
      </div>

      {/* Job List */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden shadow-sm">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-gray-800 bg-gray-800/50 text-xs font-semibold text-gray-400 uppercase tracking-wider">
              <th className="px-6 py-4">Job Name</th>
              <th className="px-6 py-4">Status</th>
              <th className="px-6 py-4">Nodes</th>
              <th className="px-6 py-4">Submitted</th>
              <th className="px-6 py-4 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-800">
            {jobs.map((job) => (
              <tr key={job.ID} className="hover:bg-gray-800/50 transition-colors group">
                <td className="px-6 py-4">
                  <div className="flex items-center">
                    <div className="p-2 rounded bg-gray-800 mr-3 group-hover:bg-gray-700 transition-colors">
                      <Terminal className="h-4 w-4 text-blue-400" />
                    </div>
                    <div>
                      <p className="text-sm font-medium">{job.Name}</p>
                      <p className="text-xs text-gray-500 font-mono uppercase tracking-tighter">{job.ID.substring(0, 8)}</p>
                    </div>
                  </div>
                </td>
                <td className="px-6 py-4">
                  <span className={cn(
                    "px-2 py-1 text-[10px] font-bold rounded-full border uppercase tracking-wider",
                    job.Status === "COMPLETED" ? "bg-green-900/30 text-green-400 border-green-800/50" :
                    job.Status === "RUNNING" ? "bg-blue-900/30 text-blue-400 border-blue-800/50 animate-pulse" :
                    job.Status === "BLOCKED" ? "bg-amber-900/30 text-amber-400 border-amber-800/50" :
                    "bg-red-900/30 text-red-400 border-red-800/50"
                  )}>
                    {job.Status}
                  </span>
                </td>
                <td className="px-6 py-4 text-sm text-gray-300">
                  {job.Stages?.length || 0} Stages
                </td>
                <td className="px-6 py-4 text-sm text-gray-500">
                  {new Date(job.CreatedAt).toLocaleTimeString()}
                </td>
                <td className="px-6 py-4 text-right">
                  <div className="flex items-center justify-end space-x-2">
                    <Link href={`/jobs/${job.ID}`} className="p-2 hover:bg-gray-700 rounded-md transition-colors text-gray-400 hover:text-white">
                      <ExternalLink className="h-4 w-4" />
                    </Link>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

