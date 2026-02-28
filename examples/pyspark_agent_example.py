#!/usr/bin/env python3
"""
Spark AI Agents - End-to-End PySpark Example

This example demonstrates all key features of the PySpark integration:
1. DataFrame integration with agent UDFs
2. Batch processing for performance
3. Structured Streaming
4. Multiple agent types
5. Production patterns

Prerequisites:
- Spark cluster (or local Spark)
- Agent server running (see docs for setup)
- pip install spark-ai-agents

Usage:
    spark-submit pyspark_agent_example.py
"""

from pyspark.sql import SparkSession
from pyspark.sql.functions import col, struct, to_json
from pyspark.sql.types import StringType, StructType, StructField

# Import spark-ai-agents
from spark_ai_agents import (
    LlmAgent,
    SequentialAgent,
    agent_udf,
    batch_agent_udf,
    AgentStreamProcessor,
    process_stream_with_agent
)


def example1_basic_dataframe_integration():
    """
    Example 1: Basic DataFrame Integration

    Apply agent to DataFrame column using UDF.
    """
    print("=" * 60)
    print("Example 1: Basic DataFrame Integration")
    print("=" * 60)

    spark = SparkSession.builder \
        .appName("AgentExample1") \
        .master("local[*]") \
        .getOrCreate()

    # Create sample data
    data = [
        ("This is a great product! Highly recommend.",),
        ("Terrible experience. Would not buy again.",),
        ("Average quality. Nothing special.",),
        ("Outstanding service and quality!",),
        ("Disappointed with the purchase.",),
    ]

    df = spark.createDataFrame(data, ["review"])
    print("\nInput Data:")
    df.show(truncate=False)

    # Create agent
    sentiment_agent = LlmAgent("sentiment-analyzer")

    # Create UDF
    analyze_sentiment = agent_udf(sentiment_agent)

    # Apply to DataFrame
    result = df.withColumn("sentiment", analyze_sentiment(df.review))

    print("\nResults with Sentiment:")
    result.show(truncate=False)

    spark.stop()


def example2_batch_processing():
    """
    Example 2: Batch Processing for Performance

    Use pandas_udf for efficient batch processing of large datasets.
    """
    print("\n" + "=" * 60)
    print("Example 2: Batch Processing")
    print("=" * 60)

    spark = SparkSession.builder \
        .appName("AgentExample2") \
        .master("local[*]") \
        .getOrCreate()

    # Create larger dataset (1000 documents)
    data = [(f"Document {i} content goes here...",) for i in range(1000)]
    df = spark.createDataFrame(data, ["content"])

    print(f"\nProcessing {df.count()} documents with batching...")

    # Create agent
    summarizer = LlmAgent("gpt-4-summarizer")

    # Create batch UDF (processes 32 rows at a time)
    summarize = batch_agent_udf(summarizer, batch_size=32)

    # Apply to DataFrame
    result = df.withColumn("summary", summarize(df.content))

    print("\nFirst 5 summaries:")
    result.select("content", "summary").show(5, truncate=50)

    print(f"\nProcessed {result.count()} documents")

    spark.stop()


def example3_dataframe_extensions():
    """
    Example 3: DataFrame Extensions

    Use the convenient DataFrame extension methods.
    """
    print("\n" + "=" * 60)
    print("Example 3: DataFrame Extensions")
    print("=" * 60)

    spark = SparkSession.builder \
        .appName("AgentExample3") \
        .master("local[*]") \
        .getOrCreate()

    # Create sample documents
    data = [
        ("News article about technology trends...",),
        ("Sports news about recent championship...",),
        ("Financial report on market performance...",),
        ("Entertainment news about new movie...",),
    ]

    df = spark.createDataFrame(data, ["content"])

    # Use DataFrame extension method (automatically registered)
    result = df.with_agent(
        "text-classifier",
        input_col="content",
        output_col="category",
        batch_size=32
    )

    print("\nClassified Documents:")
    result.show(truncate=False)

    spark.stop()


def example4_sequential_agents():
    """
    Example 4: Sequential Agents

    Chain multiple agents together for complex processing.
    """
    print("\n" + "=" * 60)
    print("Example 4: Sequential Agents")
    print("=" * 60)

    spark = SparkSession.builder \
        .appName("AgentExample4") \
        .master("local[*]") \
        .getOrCreate()

    # Create sample data
    data = [
        ("Raw unprocessed text with lots of noise and formatting issues...",),
        ("Another document that needs cleanup and processing...",),
    ]

    df = spark.createDataFrame(data, ["raw_text"])

    # Create sequential agent (preprocessor → summarizer → classifier)
    pipeline = SequentialAgent(
        "document-pipeline",
        sub_agents=["preprocessor", "summarizer", "classifier"]
    )

    # Create UDF
    process = agent_udf(pipeline)

    # Apply pipeline
    result = df.withColumn("processed", process(df.raw_text))

    print("\nPipeline Results:")
    result.show(truncate=False)

    spark.stop()


def example5_streaming():
    """
    Example 5: Structured Streaming with Agents

    Process streaming data with real-time agent execution.

    Note: This example requires a streaming source (Kafka, socket, etc.)
    For demo purposes, we use a file stream.
    """
    print("\n" + "=" * 60)
    print("Example 5: Structured Streaming")
    print("=" * 60)

    spark = SparkSession.builder \
        .appName("AgentExample5") \
        .master("local[*]") \
        .config("spark.sql.streaming.schemaInference", "true") \
        .getOrCreate()

    # Create streaming DataFrame (reading from a directory)
    # In production, this would be Kafka, Kinesis, etc.
    stream_schema = StructType([
        StructField("timestamp", StringType(), True),
        StructField("user_id", StringType(), True),
        StructField("message", StringType(), True)
    ])

    # Example: Read from JSON files in a directory
    # stream = spark.readStream \
    #     .schema(stream_schema) \
    #     .json("/path/to/streaming/data/")

    # For demo, create a static DataFrame
    demo_data = [
        ("2024-01-01 10:00:00", "user1", "This is amazing!"),
        ("2024-01-01 10:01:00", "user2", "Not happy with this."),
        ("2024-01-01 10:02:00", "user3", "It's okay, nothing special."),
    ]

    df = spark.createDataFrame(demo_data, ["timestamp", "user_id", "message"])

    print("\nDemo Streaming Data:")
    df.show()

    # Process with agent (in real streaming scenario)
    processor = AgentStreamProcessor("sentiment-analyzer", batch_size=32)
    processed = processor.process(df, input_col="message", output_col="sentiment")

    print("\nProcessed Streaming Data:")
    processed.show(truncate=False)

    # In production, you would write to a sink:
    # query = processed.writeStream \
    #     .format("kafka") \
    #     .option("kafka.bootstrap.servers", "localhost:9092") \
    #     .option("topic", "analyzed-messages") \
    #     .start()
    #
    # query.awaitTermination()

    spark.stop()


def example6_production_patterns():
    """
    Example 6: Production Patterns

    Demonstrates production-ready patterns:
    - Error handling
    - Metrics collection
    - Checkpointing
    - Resource management
    """
    print("\n" + "=" * 60)
    print("Example 6: Production Patterns")
    print("=" * 60)

    spark = SparkSession.builder \
        .appName("AgentExample6") \
        .master("local[*]") \
        .config("spark.sql.adaptive.enabled", "true") \
        .config("spark.sql.adaptive.coalescePartitions.enabled", "true") \
        .getOrCreate()

    # Create dataset
    data = [(f"Document {i}",) for i in range(100)]
    df = spark.createDataFrame(data, ["content"])

    # Repartition for optimal parallelism
    df = df.repartition(10)  # 10 partitions for 10 parallel tasks

    # Create agent with timeout and retries
    agent = LlmAgent("gpt-4-summarizer")

    # Use batch processing for efficiency
    summarize = batch_agent_udf(agent, batch_size=32)

    # Process with caching for iterative workloads
    result = df.withColumn("summary", summarize(df.content))
    result.cache()  # Cache results

    # Collect metrics
    print(f"\nTotal documents: {result.count()}")
    print(f"Partitions: {result.rdd.getNumPartitions()}")

    # Show sample results
    print("\nSample Results:")
    result.show(10, truncate=50)

    # Save results
    result.write.mode("overwrite").parquet("/tmp/agent_results")

    print("\nResults saved to /tmp/agent_results")

    spark.stop()


def main():
    """Run all examples"""
    print("\n" + "=" * 60)
    print("Spark AI Agents - PySpark Integration Examples")
    print("=" * 60)

    # Note: These examples require an agent server to be running
    # See documentation for setup instructions

    # Run examples
    example1_basic_dataframe_integration()
    example2_batch_processing()
    example3_dataframe_extensions()
    example4_sequential_agents()
    example5_streaming()
    example6_production_patterns()

    print("\n" + "=" * 60)
    print("All examples completed!")
    print("=" * 60)


if __name__ == "__main__":
    main()
