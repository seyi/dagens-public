from dagens import Graph, Agent, DagensClient
import time
import sys

def main():
    print("🚀 Initializing Dagens Client...")
    client = DagensClient(endpoint="http://localhost:8080")
    
    # 1. Define the AI Agent
    poet = Agent(
        name="deepseek-poet", 
        instruction="Write a haiku about a distributed system that never fails.",
        model="deepseek/deepseek-chat"
    )
    
    # 2. Build the Graph
    g = Graph("Haiku Generator")
    g.add_node(poet)
    g.set_entry(poet)
    g.add_finish(poet)
    
    # 3. Submit Job
    print("📤 Submitting Job to Cluster...")
    try:
        job_id = client.submit(g, instruction="Generate Poem")
        print(f"✅ Job Accepted! ID: {job_id}")
    except Exception as e:
        print(f"❌ Submission Failed: {e}")
        return

    # 4. Poll for Results
    print("⏳ Waiting for DeepSeek response...")
    while True:
        status = client.get_status(job_id)
        state = status.get("Status")
        
        if state == "COMPLETED":
            print("\n🎉 Job Completed Successfully!")
            # Extract result from the first task of the first stage
            # (In a real app, you'd traverse the graph output)
            try:
                # Navigate the JSON response structure: Stages -> Tasks -> Output -> Result
                stages = status.get("Stages", [])
                if stages:
                    tasks = stages[0].get("Tasks", [])
                    if tasks:
                        result = tasks[0].get("Output", {}).get("Result")
                        print("\n" + "="*40)
                        print("🤖 AI OUTPUT:")
                        print("="*40)
                        print(result)
                        print("="*40 + "\n")
            except Exception as e:
                print(f"⚠️ Could not parse result: {e}")
            break
            
        elif state == "FAILED":
            print("\n❌ Job Failed!")
            break
            
        sys.stdout.write(".")
        sys.stdout.flush()
        time.sleep(1)

if __name__ == "__main__":
    main()
