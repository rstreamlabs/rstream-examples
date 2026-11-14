# Update the LLM sample runtime and models

Pending an inference compatibility review.

Keep the worker on b9829 for now. The tested v1.0.0 update changes the prompt and grammar APIs used by the worker, so updating the version alone does not compile.

Adapt the worker to the new APIs, then verify model loading, streamed generation, tool calls, cancellation and cleanup before changing the pin. Existing working inference behavior must be preserved.

Review the default model and the suggested models for both the embedded worker and the Ollama setup. Select maintained models that suit each hardware tier, check their licenses and memory needs, and test their chat templates and tool calls with the selected runtime. Record exact model revisions so the results can be reproduced; retain older aliases where they still work.

Qualify Vercel AI SDK, its provider and React adapters, and the chat components together with that model/runtime pair. Cover streamed text and reasoning, tool calls, failures and cancellation through the actual application.
