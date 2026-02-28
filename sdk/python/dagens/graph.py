from typing import List, Dict, Any, Optional
from pydantic import BaseModel, Field
import uuid

class AgentNodeConfig(BaseModel):
    model: Optional[str] = None
    instruction: Optional[str] = None
    tools: List[str] = Field(default_factory=list)


class PolicyRuleDefinition(BaseModel):
    """Definition of a single policy rule"""
    id: str
    name: Optional[str] = None
    type: str  # "pii", "content", "length", "regex"
    action: str  # "pass", "block", "redact", "warn"
    severity: Optional[str] = None  # "critical", "high", "medium", "low"
    enabled: bool = True
    config: Dict[str, Any] = Field(default_factory=dict)


class PolicyNodeConfig(BaseModel):
    """Configuration for policy/guardrail nodes"""
    rules: List[PolicyRuleDefinition] = Field(default_factory=list)
    fail_open: bool = Field(False, alias="fail_open")
    stop_on_first_match: bool = Field(False, alias="stop_on_first_match")

    class Config:
        populate_by_name = True


class NodeDefinition(BaseModel):
    id: str
    type: str  # "function", "agent", "parallel", "loop", "conditional", "policy"
    name: Optional[str] = None
    metadata: Dict[str, Any] = Field(default_factory=dict)
    agent_config: Optional[AgentNodeConfig] = Field(None, alias="agent_config")
    policy_config: Optional[PolicyNodeConfig] = Field(None, alias="policy_config")
    children: List[str] = Field(default_factory=list)

class EdgeDefinition(BaseModel):
    from_node: str = Field(..., alias="from")
    to_node: str = Field(..., alias="to")

    class Config:
        populate_by_name = True

class JobInput(BaseModel):
    instruction: str
    data: Dict[str, Any] = Field(default_factory=dict)

class JobSubmissionRequest(BaseModel):
    name: str
    description: Optional[str] = None
    nodes: List[NodeDefinition] = Field(default_factory=list)
    edges: List[EdgeDefinition] = Field(default_factory=list)
    entry_node: str = Field(..., alias="entry_node")
    finish_nodes: List[str] = Field(default_factory=list, alias="finish_nodes")
    input: JobInput
    # SessionID enables sticky scheduling. When provided, all tasks in this job
    # will be routed to the same worker as previous jobs with this session_id,
    # preserving in-memory context on that worker.
    session_id: Optional[str] = Field(None, alias="session_id")

class Node:
    """High-level abstraction for a Graph Node"""
    def __init__(self, id: str = None, type: str = "function", name: str = None, metadata: Dict[str, Any] = None):
        self.id = id or str(uuid.uuid4())
        self.type = type
        self.name = name or self.id
        self.metadata = metadata or {}

    def to_definition(self) -> NodeDefinition:
        return NodeDefinition(
            id=self.id,
            type=self.type,
            name=self.name,
            metadata=self.metadata
        )

class Agent(Node):
    """Abstraction for an AI Agent Node"""
    def __init__(self, name: str, model: str = "gpt-4", instruction: str = None, tools: List[str] = None, **kwargs):
        super().__init__(id=name, type="agent", name=name, **kwargs)
        self.model = model
        self.instruction = instruction
        self.tools = tools or []

    def to_definition(self) -> NodeDefinition:
        defn = super().to_definition()
        defn.agent_config = AgentNodeConfig(
            model=self.model,
            instruction=self.instruction,
            tools=self.tools
        )
        return defn


class Policy(Node):
    """
    Policy/Guardrail Node for filtering and validating agent outputs.

    Provides a fluent API for configuring policy rules:

        policy = Policy("output-filter")
            .add_pii_filter(action="redact", types=["email", "phone", "ssn"])
            .add_content_filter(action="block", keywords=["secret", "confidential"])
            .add_length_limit(max_length=10000)
    """
    def __init__(self, name: str, fail_open: bool = False, stop_on_first_match: bool = False, **kwargs):
        super().__init__(id=name, type="policy", name=name, **kwargs)
        self.rules: List[PolicyRuleDefinition] = []
        self.fail_open = fail_open
        self.stop_on_first_match = stop_on_first_match
        self._rule_counter = 0

    def _next_rule_id(self, prefix: str) -> str:
        self._rule_counter += 1
        return f"{self.id}-{prefix}-{self._rule_counter}"

    def add_pii_filter(
        self,
        action: str = "redact",
        types: List[str] = None,
        severity: str = "high",
        enabled: bool = True
    ) -> "Policy":
        """
        Add PII detection rule.

        Args:
            action: "pass", "block", "redact", or "warn"
            types: PII types to detect: "email", "phone", "ssn", "credit_card", "ip_address"
                   Defaults to all types if not specified.
            severity: "critical", "high", "medium", "low"
            enabled: Whether rule is active
        """
        config = {}
        if types:
            config["types"] = types

        self.rules.append(PolicyRuleDefinition(
            id=self._next_rule_id("pii"),
            name="PII Detection",
            type="pii",
            action=action,
            severity=severity,
            enabled=enabled,
            config=config
        ))
        return self

    def add_content_filter(
        self,
        action: str = "block",
        keywords: List[str] = None,
        categories: List[str] = None,
        case_sensitive: bool = False,
        severity: str = "high",
        enabled: bool = True
    ) -> "Policy":
        """
        Add content/keyword filtering rule.

        Args:
            action: "pass", "block", "redact", or "warn"
            keywords: List of keywords to match
            categories: Predefined categories: "profanity", "violence", "harmful"
            case_sensitive: Whether matching is case-sensitive
            severity: "critical", "high", "medium", "low"
            enabled: Whether rule is active
        """
        config = {"case_sensitive": case_sensitive}
        if keywords:
            config["keywords"] = keywords
        if categories:
            config["categories"] = categories

        self.rules.append(PolicyRuleDefinition(
            id=self._next_rule_id("content"),
            name="Content Filter",
            type="content",
            action=action,
            severity=severity,
            enabled=enabled,
            config=config
        ))
        return self

    def add_length_limit(
        self,
        max_length: int = 10000,
        action: str = "redact",
        severity: str = "medium",
        enabled: bool = True
    ) -> "Policy":
        """
        Add output length limit rule.

        Args:
            max_length: Maximum allowed character count
            action: "pass", "block", "redact", or "warn"
            severity: "critical", "high", "medium", "low"
            enabled: Whether rule is active
        """
        self.rules.append(PolicyRuleDefinition(
            id=self._next_rule_id("length"),
            name="Length Limit",
            type="length",
            action=action,
            severity=severity,
            enabled=enabled,
            config={"max_length": max_length}
        ))
        return self

    def add_regex_filter(
        self,
        pattern: str,
        action: str = "redact",
        replacement: str = "[REDACTED]",
        severity: str = "medium",
        enabled: bool = True,
        name: str = None
    ) -> "Policy":
        """
        Add custom regex pattern matching rule.

        Args:
            pattern: Regular expression pattern to match
            action: "pass", "block", "redact", or "warn"
            replacement: Text to use when redacting matches
            severity: "critical", "high", "medium", "low"
            enabled: Whether rule is active
            name: Optional custom name for this rule
        """
        self.rules.append(PolicyRuleDefinition(
            id=self._next_rule_id("regex"),
            name=name or "Regex Filter",
            type="regex",
            action=action,
            severity=severity,
            enabled=enabled,
            config={"pattern": pattern, "replacement": replacement}
        ))
        return self

    def add_llm_filter(
        self,
        rubric: str = None,
        threshold: float = 0.7,
        action: str = "block",
        severity: str = "high",
        enabled: bool = True,
        name: str = None
    ) -> "Policy":
        """
        Add semantic evaluation rule using a secondary LLM check.

        Args:
            rubric: The rubric to evaluate against (e.g., "Check for toxicity")
            threshold: Violation threshold (0.0 to 1.0)
            action: "pass", "block", or "warn"
            severity: "critical", "high", "medium", "low"
            enabled: Whether rule is active
            name: Optional custom name for this rule
        """
        config = {"threshold": threshold}
        if rubric:
            config["rubric"] = rubric

        self.rules.append(PolicyRuleDefinition(
            id=self._next_rule_id("llm"),
            name=name or "Semantic LLM Filter",
            type="llm",
            action=action,
            severity=severity,
            enabled=enabled,
            config=config
        ))
        return self

    def add_rule(self, rule: PolicyRuleDefinition) -> "Policy":
        """Add a custom rule definition directly."""
        self.rules.append(rule)
        return self

    def to_definition(self) -> NodeDefinition:
        defn = super().to_definition()
        defn.policy_config = PolicyNodeConfig(
            rules=self.rules,
            fail_open=self.fail_open,
            stop_on_first_match=self.stop_on_first_match
        )
        return defn


class Graph:
    """Distributed Agent Graph"""
    def __init__(self, name: str, description: str = None):
        self.name = name
        self.description = description
        self.nodes: Dict[str, Node] = {}
        self.edges: List[tuple] = []
        self.entry_node: Optional[str] = None
        self.finish_nodes: List[str] = []

    def add_node(self, node: Node):
        self.nodes[node.id] = node
        if not self.entry_node:
            self.entry_node = node.id
        return node

    def add_edge(self, from_node: Node, to_node: Node):
        if from_node.id not in self.nodes:
            self.add_node(from_node)
        if to_node.id not in self.nodes:
            self.add_node(to_node)
        self.edges.append((from_node.id, to_node.id))

    def set_entry(self, node: Node):
        self.entry_node = node.id

    def add_finish(self, node: Node):
        if node.id not in self.finish_nodes:
            self.finish_nodes.append(node.id)

    def compile(self, instruction: str, data: Dict[str, Any] = None, session_id: str = None) -> JobSubmissionRequest:
        """
        Translates the high-level Graph into a JSON-serializable request.

        Args:
            instruction: The instruction to execute
            data: Optional input data dictionary
            session_id: Optional session ID for sticky scheduling.
                        When provided, all tasks in this job will be routed to the same
                        worker as previous jobs with this session_id, preserving
                        in-memory context on that worker.
        """
        if not self.entry_node:
            raise ValueError("Graph must have an entry node")

        if not self.finish_nodes and self.nodes:
            # Fallback: last node added
            self.finish_nodes = [list(self.nodes.keys())[-1]]

        return JobSubmissionRequest(
            name=self.name,
            description=self.description,
            nodes=[n.to_definition() for n in self.nodes.values()],
            edges=[EdgeDefinition(**{"from": e[0], "to": e[1]}) for e in self.edges],
            entry_node=self.entry_node,
            finish_nodes=self.finish_nodes,
            input=JobInput(instruction=instruction, data=data or {}),
            session_id=session_id
        )
