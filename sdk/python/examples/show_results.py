from dagens import Graph, Agent, DagensClient
import time
import sys

def main():
    # 1. Initialize Client
    client = DagensClient(endpoint="http://localhost:8080")
    
    # 2. Define a Creative Agent
    poet = Agent(
        name="ai-poet", 
        instruction="Write a short, professional 2-sentence summary of the benefits of distributed AI.",
        model="deepseek/deepseek-chat"
    )
    
    # 3. Build Graph
    g = Graph("Output Showcase")
    g.add_node(poet)
    
    # 4. Submit Job
    print("🚀 Submitting job to Dagens Cluster...")
    job_id = client.submit(g, instruction="Explain distributed AI")
    print(f"✅ Job ID: {job_id}")
    
    # 5. Wait and Retrieve Output
    print("\n⏳ Waiting for AI to respond (polling)...")
    while True:
        status_data = client.get_status(job_id)
        status = status_data.get("Status")
        
        if status == "COMPLETED":
            print("\n🎊 Job Finished!")
            
            # Extract output from the tasks in the job
            # The Go 'Job' struct contains 'Stages' which contain 'Tasks'
            for stage in status_data.get("Stages", []):
                for task in stage.get("Tasks", []):
                    agent_id = task.get("AgentID")
                    result = task.get("Output", {}).get("Result")
                    
                    print("-" * 50)
                    print(f"🤖 Output from Agent [{agent_id}]:")
                    print(f"\n{result}\n")
                    print("-" * 50)
            break
            
        elif status == "FAILED":
            print(f"\n❌ Job failed!")
            break
            
        sys.stdout.write(".")
        sys.stdout.flush()
        time.Sleep(1)

if __name__ == "__main__":
    # Ensure correct python path for sdk
    main()
