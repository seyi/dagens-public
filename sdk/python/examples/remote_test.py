from dagens import Graph, Agent, DagensClient
import json

def main():
    # 1. Define the workflow
    planner = Agent(
        name="planner", 
        instruction="Break down the topic into sub-tasks",
        model="gpt-4"
    )
    
    worker = Agent(
        name="researcher",
        instruction="Search for detailed information on each sub-task",
        model="gpt-4"
    )
    
    # 2. Build the Graph
    g = Graph("Distributed Market Research")
    g.add_edge(planner, worker)
    
    # 3. Compile to check JSON structure
    # This matches the schema in pkg/api/types.go
    req = g.compile(instruction="Analyze the impact of quantum computing on cybersecurity")
    print("--- Compiled JSON Payload ---")
    print(json.dumps(req.dict(by_alias=True), indent=2))
    
    # 4. Optional: Submit if cluster is running
    try:
        client = DagensClient(endpoint="http://localhost:8080")
        job_id = client.submit(g, instruction="Remote test from Python")
        print(f"\n✅ Job submitted successfully! ID: {job_id}")

        print("\nWaiting for job completion...")
        import time
        for _ in range(10):
            status = client.get_status(job_id)
            print(f"Status: {status.get('Status')}")
            if status.get('Status') in ['COMPLETED', 'FAILED']:
                print("\n--- Results ---")
                print(json.dumps(status, indent=2))
                break
            time.sleep(2)
            
    except Exception as e:
        print(f"\n❌ Could not submit to cluster (is it running?): {e}")

if __name__ == "__main__":
    main()

