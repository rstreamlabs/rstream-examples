// Thin C++ shim exposing llama.cpp's common_chat tool-calling pipeline to Go.
//
// We *use* llama.cpp, we do not reimplement it. One chat turn is:
//   OpenAI JSON -> common_chat_msgs/tools_parse_oaicompat
//               -> common_chat_templates_apply  (prompt + lazy grammar + format)
//               -> generate with common_sampler carrying that grammar
//               -> common_chat_peg_parse        (structured content + tool_calls)
// This is the same machinery llama-server uses, so tool-call detection and
// parsing are robust by construction (no hand-rolled <tool_call> scanning).
//
// A model holds shared weights and templates; contexts are per-request decoders.
// Multiple contexts decode concurrently (llama.cpp's own thread-safety test does
// exactly this). Each turn is stateless: the context's KV cache is cleared first.
#include "shim.h"
#include "chat.h"
#include "common.h"
#include "llama.h"
#include "peg-parser.h"
#include "sampling.h"
#include <nlohmann/json.hpp>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

using json = nlohmann::ordered_json;

struct pllm_model {
    llama_model *model = nullptr;
    common_chat_templates_ptr tmpls;
};

struct pllm_context {
    pllm_model *m = nullptr;
    llama_context *ctx = nullptr;
};

static char *dup_cstr(const std::string &s) {
    char *out = (char *)malloc(s.size() + 1);
    memcpy(out, s.data(), s.size());
    out[s.size()] = '\0';
    return out;
}

static char *error_json(const std::string &msg) {
    json j;
    j["error"] = msg;
    return dup_cstr(j.dump());
}

// llama.cpp logs verbosely to stderr by default: the model-loader metadata dump
// on load, and a per-token grammar-trigger trace during constrained decoding.
// Forward only warnings and errors so the worker's own structured logs stay
// legible. CONT lines continue the previous message, so they follow its level.
static void pllm_log_cb(ggml_log_level level, const char *text, void * /*user*/) {
    static ggml_log_level last = GGML_LOG_LEVEL_NONE;
    bool show;
    if (level == GGML_LOG_LEVEL_CONT) {
        show = last == GGML_LOG_LEVEL_WARN || last == GGML_LOG_LEVEL_ERROR;
    } else {
        show = level == GGML_LOG_LEVEL_WARN || level == GGML_LOG_LEVEL_ERROR;
        last = level;
    }
    if (show) {
        fputs(text, stderr);
    }
}

pllm_model *pllm_model_load(const char *model_path) {
    static bool backend_ready = false;
    if (!backend_ready) {
        llama_log_set(pllm_log_cb, nullptr);
        ggml_log_set(pllm_log_cb, nullptr);
        llama_backend_init();
        backend_ready = true;
    }
    llama_model_params mparams = llama_model_default_params();
    mparams.n_gpu_layers = 999; // offload all layers when a GPU backend is linked (Metal/CUDA); ignored on CPU-only builds
    llama_model *model = llama_model_load_from_file(model_path, mparams);
    if (!model) return nullptr;
    auto *m = new pllm_model();
    m->model = model;
    m->tmpls = common_chat_templates_init(model, "");
    return m;
}

void pllm_model_free(pllm_model *m) {
    if (!m) return;
    if (m->model) llama_model_free(m->model);
    delete m;
}

pllm_context *pllm_context_new(pllm_model *m, int n_ctx) {
    llama_context_params cparams = llama_context_default_params();
    cparams.n_ctx = n_ctx;
    cparams.n_batch = n_ctx;
    llama_context *ctx = llama_init_from_model(m->model, cparams);
    if (!ctx) return nullptr;
    auto *c = new pllm_context();
    c->m = m;
    c->ctx = ctx;
    return c;
}

void pllm_context_free(pllm_context *c) {
    if (!c) return;
    if (c->ctx) llama_free(c->ctx);
    delete c;
}

char *pllm_chat(pllm_context *c, const char *messages_json, const char *tools_json,
                int max_tokens, float temp,
                pllm_content_cb cb, uintptr_t cb_user,
                pllm_stop_cb stop, uintptr_t stop_user) {
    try {
        llama_model *model = c->m->model;
        llama_context *lctx = c->ctx;
        const llama_vocab *vocab = llama_model_get_vocab(model);
        // Stateless turn on a possibly-reused context: reset the KV cache. Cancellation
        // is checked per token below (on this thread — never from ggml worker threads).
        llama_memory_clear(llama_get_memory(lctx), true);
        common_chat_templates_inputs inputs;
        inputs.messages = common_chat_msgs_parse_oaicompat(json::parse(messages_json));
        if (tools_json && *tools_json) {
            inputs.tools = common_chat_tools_parse_oaicompat(json::parse(tools_json));
        }
        inputs.tool_choice = COMMON_CHAT_TOOL_CHOICE_AUTO;
        inputs.add_generation_prompt = true;
        inputs.use_jinja = true;
        common_chat_params cp = common_chat_templates_apply(c->m->tmpls.get(), inputs);
        common_params_sampling sp;
        sp.temp = temp;
        if (!cp.grammar.empty()) {
            sp.grammar = common_grammar(COMMON_GRAMMAR_TYPE_TOOL_CALLS, cp.grammar);
        }
        sp.grammar_lazy = cp.grammar_lazy;
        sp.grammar_triggers = cp.grammar_triggers;
        for (const auto &tokstr : cp.preserved_tokens) {
            for (auto t : common_tokenize(vocab, tokstr, false, true)) {
                sp.preserved_tokens.insert(t);
            }
        }
        common_sampler *smpl = common_sampler_init(model, sp);
        common_chat_parser_params pp(cp);
        common_peg_arena arena;
        arena.load(cp.parser);
        std::vector<llama_token> prompt = common_tokenize(lctx, cp.prompt, true, true);
        const int n_ctx = (int)llama_n_ctx(lctx);
        const int n_batch = (int)llama_n_batch(lctx);
        if ((int)prompt.size() >= n_ctx) {
            common_sampler_free(smpl);
            return error_json("prompt is larger than the context window (" +
                              std::to_string(prompt.size()) + " >= " +
                              std::to_string(n_ctx) + " tokens); raise --ctx or shorten the input");
        }
        // Decode the prompt in n_batch-sized chunks so a large prompt (e.g. a big
        // tool result) never exceeds a single batch.
        for (int i = 0; i < (int)prompt.size(); i += n_batch) {
            int n = (int)prompt.size() - i;
            if (n > n_batch) n = n_batch;
            if (llama_decode(lctx, llama_batch_get_one(prompt.data() + i, n))) {
                common_sampler_free(smpl);
                return error_json("failed to decode prompt");
            }
        }
        int n_past = (int)prompt.size();
        std::string out, streamed;
        llama_token cur = 0;
        int n_gen = 0;
        // max_tokens <= 0 means generate until the model stops (EOS) or the
        // context fills — the same open-ended default as llama-server and the
        // OpenAI API, so a normal answer is never truncated at a fixed cap.
        for (int i = 0; (max_tokens <= 0 || i < max_tokens) && n_past < n_ctx; i++) {
            if (stop && stop(stop_user)) break;
            cur = common_sampler_sample(smpl, lctx, -1);
            common_sampler_accept(smpl, cur, true);
            if (llama_vocab_is_eog(vocab, cur)) break;
            out += common_token_to_piece(lctx, cur, true);
            n_gen++;
            if (cb) {
                common_chat_msg pmsg = common_chat_peg_parse(arena, out, true, pp);
                if (pmsg.content.size() > streamed.size() &&
                    pmsg.content.compare(0, streamed.size(), streamed) == 0) {
                    cb(cb_user, pmsg.content.c_str() + streamed.size());
                }
                streamed = pmsg.content;
            }
            if (llama_decode(lctx, llama_batch_get_one(&cur, 1))) break;
            n_past++;
        }
        common_sampler_free(smpl);
        common_chat_msg msg = common_chat_peg_parse(arena, out, false, pp);
        json result;
        result["content"] = msg.content;
        json calls = json::array();
        for (const auto &tc : msg.tool_calls) {
            calls.push_back({{"name", tc.name}, {"arguments", tc.arguments}});
        }
        result["tool_calls"] = calls;
        result["usage"] = {{"prompt_tokens", (int)prompt.size()}, {"completion_tokens", n_gen}};
        return dup_cstr(result.dump());
    } catch (const std::exception &ex) {
        return error_json(ex.what());
    }
}

void pllm_free(char *s) { free(s); }
