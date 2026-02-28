"use client";

import React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { cn } from "@/lib/utils";
import { 
  LayoutDashboard, 
  Network, 
  Activity, 
  Settings, 
  Terminal,
  Cpu
} from "lucide-react";

const navigation = [
  { name: "Dashboard", href: "/", icon: LayoutDashboard },
  { name: "Agent Jobs", href: "/jobs", icon: Terminal },
  { name: "Cluster Nodes", href: "/nodes", icon: Cpu },
  { name: "Tracing", href: "/tracing", icon: Activity },
];

export function Sidebar() {
  const pathname = usePathname();

  return (
    <div className="flex h-full w-64 flex-col bg-gray-900 text-white border-r border-gray-800">
      <div className="flex h-16 shrink-0 items-center px-6">
        <Network className="h-8 w-8 text-blue-500 mr-2" />
        <span className="text-xl font-bold tracking-tight">DAGENS</span>
      </div>
      <nav className="flex flex-1 flex-col px-4 py-4 space-y-1">
        {navigation.map((item) => {
          const isActive = pathname === item.href;
          return (
            <Link
              key={item.name}
              href={item.href}
              className={cn(
                "group flex items-center px-3 py-2 text-sm font-medium rounded-md transition-colors",
                isActive 
                  ? "bg-blue-600 text-white" 
                  : "text-gray-300 hover:bg-gray-800 hover:text-white"
              )}
            >
              <item.icon className={cn(
                "mr-3 h-5 w-5 shrink-0",
                isActive ? "text-white" : "text-gray-400 group-hover:text-white"
              )} />
              {item.name}
            </Link>
          );
        })}
      </nav>
      <div className="p-4 border-t border-gray-800">
        <div className="flex items-center text-xs text-gray-500 uppercase font-semibold mb-2">
          System Status
        </div>
        <div className="flex items-center">
          <div className="h-2 w-2 rounded-full bg-green-500 mr-2 animate-pulse" />
          <span className="text-sm text-gray-300">Cluster Connected</span>
        </div>
      </div>
    </div>
  );
}
