"use client";

import React, { useMemo } from 'react';
import { 
  ReactFlow, 
  Background, 
  Controls, 
  useNodesState, 
  useEdgesState,
  Node,
  Edge,
  MarkerType
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';

// Type definitions matching our Go API
interface DagensNode {
  id: string;
  type: string;
  name?: string;
  status?: string; // Added status field
  metadata?: Record<string, any>;
}

interface DagensEdge {
  from: string;
  to: string;
}

interface JobData {
  nodes: DagensNode[];
  edges: DagensEdge[];
}

interface JobGraphProps {
  data: JobData;
}

// Helper to get color based on status
const getStatusColor = (status?: string) => {
  switch (status) {
    case "COMPLETED": return "#10B981"; // Green-500
    case "RUNNING": return "#3B82F6";   // Blue-500
    case "BLOCKED": return "#F59E0B";   // Amber-500
    case "FAILED": return "#EF4444";    // Red-500
    default: return "#374151";          // Gray-700
  }
};

export function JobGraph({ data }: JobGraphProps) {
  // Transform Dagens data to React Flow format
  const initialNodes: Node[] = useMemo(() => {
    return data.nodes.map((node, index) => ({
      id: node.id,
      position: { x: 250, y: index * 100 + 50 },
      data: { label: node.name || node.id },
      style: {
        background: '#111827',
        color: '#fff',
        border: `2px solid ${getStatusColor(node.status)}`, // Dynamic border color
        borderRadius: '8px',
        padding: '10px',
        minWidth: '150px',
        textAlign: 'center',
        boxShadow: node.status === 'RUNNING' ? `0 0 15px ${getStatusColor(node.status)}40` : 'none',
        transition: 'all 0.3s ease',
      },
    }));
  }, [data.nodes]);

  // Transform Dagens edges to React Flow format
  const initialEdges: Edge[] = useMemo(() => {
    return data.edges.map((edge) => ({
      id: `e-${edge.from}-${edge.to}`,
      source: edge.from,
      target: edge.to,
      animated: true,
      style: { stroke: '#374151', strokeWidth: 2 },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: '#374151',
      },
    }));
  }, [data.edges]);

  const [nodes, , onNodesChange] = useNodesState(initialNodes);
  const [edges, , onEdgesChange] = useEdgesState(initialEdges);

  return (
    <div className="h-[500px] w-full bg-gray-950 border border-gray-800 rounded-xl overflow-hidden">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        fitView
        colorMode="dark"
      >
        <Background color="#374151" gap={16} />
        <Controls className="bg-gray-800 border-gray-700 fill-white" />
      </ReactFlow>
    </div>
  );
}
