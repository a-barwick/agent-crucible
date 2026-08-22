from typing import Any, Optional

from langchain_core.callbacks import CallbackManagerForLLMRun
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, BaseMessage
from langchain_core.outputs import ChatGeneration, ChatResult
from pydantic import Field

from .intent import parse_intent


class CloserPlanner(BaseChatModel):
    """A real LangChain chat model. Deterministic JSON intent by default."""

    companies: list[str] = Field(default_factory=list)
    partial: bool = False

    @property
    def _llm_type(self) -> str:
        return "crucible-planner"

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: Optional[list[str]] = None,
        run_manager: Optional[CallbackManagerForLLMRun] = None,
        **kwargs: Any,
    ) -> ChatResult:
        text = ""
        if messages:
            text = getattr(messages[-1], "content", "") or ""
        intent = parse_intent(text, self.companies)
        if self.partial:
            intent["notify"] = False
        import json

        body = json.dumps(intent)
        return ChatResult(generations=[ChatGeneration(message=AIMessage(content=body))])
