import requests
from typing import Dict, Any, Optional
from .graph import Graph, JobSubmissionRequest

class SafetyViolationError(RuntimeError):
    """Raised when a job is blocked by a safety policy"""
    def __init__(self, message: str, job_id: str, details: Optional[Dict[str, Any]] = None):
        super().__init__(message)
        self.job_id = job_id
        self.details = details

class DagensClient:
    """HTTP Client for the Dagens Control Plane"""
    def __init__(self, endpoint: str = "http://localhost:8080"):
        self.endpoint = endpoint.rstrip("/")

    def submit(self, graph: Graph, instruction: str, data: Dict[str, Any] = None, session_id: str = None) -> str:
        """
        Submits a graph for distributed execution.
        Returns the Job ID.
        """
        # 1. Compile the graph to the JSON-compatible request model
        req = graph.compile(instruction, data, session_id=session_id)
        
        # 2. Serialize to dict (handling Pydantic aliases like 'from')
        payload = req.dict(by_alias=True)

        # 3. Post to API
        url = f"{self.endpoint}/v1/jobs"
        response = requests.post(url, json=payload)
        
        if response.status_code != 202:
            raise RuntimeError(f"Failed to submit job: {response.status_code} - {response.text}")

        data = response.json()
        return data.get("job_id")

    def get_status(self, job_id: str) -> Dict[str, Any]:
        """Fetch the current status of a job"""
        url = f"{self.endpoint}/v1/jobs/{job_id}"
        response = requests.get(url)
        
        if response.status_code != 200:
            raise RuntimeError(f"Failed to get job status: {response.status_code}")
            
        data = response.json()

        # Check for safety block
        if data.get("Status") == "BLOCKED":
            # Extract reason from metadata if available
            reason = data.get("Metadata", {}).get("policy_block_reason", "Safety policy violation")
            raise SafetyViolationError(reason, job_id, data)

        return data