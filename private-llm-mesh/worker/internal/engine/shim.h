#ifndef PLLM_SHIM_H
#define PLLM_SHIM_H
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif

// A model holds the shared weights and chat templates; contexts are per-request
// decoders created from it. Multiple contexts decode concurrently (llama.cpp's
// own thread-safety test does exactly this).
typedef struct pllm_model pllm_model;
typedef struct pllm_context pllm_context;

// Content callback: incremental assistant CONTENT as it generates (UTF-8, may be
// empty). Tool-call markup never appears here; tool calls arrive in the result.
typedef void (*pllm_content_cb)(uintptr_t user, const char *delta);

// Stop callback: returns non-zero to abort the current turn (client gone or
// deadline reached). Checked per token and used as llama.cpp's abort callback.
typedef int (*pllm_stop_cb)(uintptr_t user);

pllm_model *pllm_model_load(const char *model_path);
void pllm_model_free(pllm_model *m);

pllm_context *pllm_context_new(pllm_model *m, int n_ctx);
void pllm_context_free(pllm_context *c);

// Run one stateless chat turn on a context. messages_json and tools_json are
// OpenAI-format JSON (tools may be NULL/empty); temp <= 0 is greedy. cb (may be
// NULL) receives content deltas; stop (may be NULL) aborts generation. Returns a
// malloc'd JSON string the caller frees with pllm_free:
//   {"content":..,"tool_calls":[..],"usage":{..}}  or  {"error":..}.
// Tool-call decision and parsing are llama.cpp's common_chat, not ours.
char *pllm_chat(pllm_context *c, const char *messages_json, const char *tools_json,
                int max_tokens, float temp,
                pllm_content_cb cb, uintptr_t cb_user,
                pllm_stop_cb stop, uintptr_t stop_user);

void pllm_free(char *s);

#ifdef __cplusplus
}
#endif
#endif
