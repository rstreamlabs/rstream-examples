const maxCandidatesPerGeneration = 64;
const maxResponseBytes = 256 * 1024;
const maxRedirects = 3;
const gatherTimeoutMs = 10_000;
const patchBatchDelayMs = 20;
const maximumCloseTimeoutMs = 5_000;
const defaultRequestTimeoutMs = 15_000;
const credentialRefreshLeadMs = 30_000;
const credentialRefreshRetryInitialMs = 1_000;
const credentialRefreshRetryMaximumMs = 10_000;
const maximumTimerDelayMs = 2_147_483_647;
const httpMonths = new Map([
  ["Jan", 0],
  ["Feb", 1],
  ["Mar", 2],
  ["Apr", 3],
  ["May", 4],
  ["Jun", 5],
  ["Jul", 6],
  ["Aug", 7],
  ["Sep", 8],
  ["Oct", 9],
  ["Nov", 10],
  ["Dec", 11],
]);

type Fetch = typeof fetch;

type WHEPClientOptions = {
  allowInsecureHTTP?: boolean;
  allowLegacyWildcardETag?: boolean;
  authorization: string;
  credentialExpiresAt?: string;
  endpoint: string;
  fetch?: Fetch;
  iceServers: RTCIceServer[];
  iceCredentialExpiresAt?: string;
  onClose?: (result: WHEPCloseResult) => void;
  onError: (error: Error) => void;
  onTrack: (event: RTCTrackEvent) => void;
  peerFactory?: (configuration: RTCConfiguration) => RTCPeerConnection;
  requestTimeoutMs?: number;
  refreshCredentials?: (signal: AbortSignal) => Promise<WHEPCredentialRefresh>;
  trustedRedirectOrigins?: string[];
};

export type WHEPCredentialRefresh = {
  authorization: string;
  endpoint: string;
  expiresAt: string;
  iceServers: RTCIceServer[];
  iceExpiresAt: string;
};

export type WHEPCloseResult = {
  credentialRefreshFailed: boolean;
  durationMilliseconds: number;
  outcome:
    | "already-absent"
    | "deleted"
    | "http-error"
    | "not-established"
    | "request-error"
    | "timed-out";
  status?: number;
};

export class WHEPHTTPError extends Error {
  readonly retryAfterMilliseconds: number | null;
  readonly status: number;

  constructor(message: string, response: Response) {
    super(`${message} (${response.status})`);
    this.name = "WHEPHTTPError";
    this.status = response.status;
    this.retryAfterMilliseconds = parseRetryAfter(
      response.headers.get("retry-after"),
    );
  }
}

function authorizationHeaders(authorization: string): Record<string, string> {
  return authorization.trim() ? { Authorization: authorization } : {};
}

type CandidateGeneration = {
  candidates: RTCIceCandidateInit[];
  complete: boolean;
  completionSent: boolean;
  count: number;
  embedded: Set<string>;
  generation: number;
  waiters: (() => void)[];
};

type ResponseWithURL = {
  response: Response;
  url: URL;
};

type RemoteDeleteResult = {
  outcome:
    "already-absent" | "deleted" | "http-error" | "request-error" | "timed-out";
  status?: number;
};

export class WHEPClient {
  readonly peer: RTCPeerConnection;
  private authorization: string;
  private endpoint: URL;
  private readonly fetch: Fetch;
  private readonly onClose?: (result: WHEPCloseResult) => void;
  private readonly onError: (error: Error) => void;
  private readonly closeTimeoutMs: number;
  private readonly requestTimeoutMs: number;
  private readonly refreshCredentials?: (
    signal: AbortSignal,
  ) => Promise<WHEPCredentialRefresh>;
  private readonly trustedRedirectOrigins: Set<string>;
  private readonly allowLegacyWildcardETag: boolean;
  private readonly allowInsecureHTTP: boolean;
  private abort = new AbortController();
  private closed = false;
  private closePromise: Promise<WHEPCloseResult> | null = null;
  private credentialExpiresAt: number | null;
  private credentialRefreshPromise: Promise<void> | null = null;
  private credentialRefreshRetryMs = credentialRefreshRetryInitialMs;
  private errorReported = false;
  private etag: string | null = null;
  private generation = newGeneration(0);
  private iceCredentialExpiresAt: number | null;
  private iceRestartTimer: ReturnType<typeof setTimeout> | null = null;
  private initialRequest: Promise<void> | null = null;
  private patchTimer: ReturnType<typeof setTimeout> | null = null;
  private patchQueue: Promise<void> = Promise.resolve();
  private remoteDeletePromise: Promise<RemoteDeleteResult> | null = null;
  private restartPromise: Promise<void> | null = null;
  private restartPending = false;
  private sessionURL: URL | null = null;
  private sessionHeaders = new Headers();
  private started = false;

  constructor(options: WHEPClientOptions) {
    this.allowInsecureHTTP = options.allowInsecureHTTP ?? false;
    this.endpoint = secureURL(options.endpoint, this.allowInsecureHTTP);
    this.authorization = normalizeAuthorization(options.authorization);
    this.credentialExpiresAt = parseCredentialExpiration(
      options.credentialExpiresAt,
    );
    this.iceCredentialExpiresAt = parseCredentialExpiration(
      options.iceCredentialExpiresAt,
    );
    this.fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.onClose = options.onClose;
    this.onError = options.onError;
    this.refreshCredentials = options.refreshCredentials;
    this.requestTimeoutMs = positiveDuration(
      options.requestTimeoutMs ?? defaultRequestTimeoutMs,
      "WHEP request timeout",
    );
    this.closeTimeoutMs = Math.min(
      this.requestTimeoutMs,
      maximumCloseTimeoutMs,
    );
    this.allowLegacyWildcardETag = options.allowLegacyWildcardETag ?? false;
    this.trustedRedirectOrigins = new Set([
      this.endpoint.origin,
      ...(options.trustedRedirectOrigins ?? []),
    ]);
    this.peer = (options.peerFactory ?? defaultPeerFactory)({
      bundlePolicy: "max-bundle",
      iceServers: options.iceServers,
    });
    this.peer.onicecandidate = (event) => this.handleLocalCandidate(event);
    this.peer.ontrack = options.onTrack;
  }

  async start() {
    this.requireOpen();
    if (this.started) {
      throw new Error("WHEP client was already started");
    }
    this.started = true;
    try {
      await this.ensureFreshCredentials(this.abort.signal, false);
      this.peer.addTransceiver("video", { direction: "recvonly" });
      const offer = await this.peer.createOffer();
      await this.peer.setLocalDescription(offer);
      this.requireOpen();
      const local = requireLocalDescription(this.peer);
      captureEmbeddedCandidates(this.generation, local.sdp);
      const settleInitialRequest = this.beginInitialRequest();
      let result: ResponseWithURL | null = null;
      try {
        result = await this.requestEndpoint(local.sdp);
        if (result.response.status === 201 || result.response.status === 406) {
          this.captureSessionURL(result);
        }
      } catch (error) {
        if (result) {
          await discardResponse(result.response);
        }
        throw error;
      } finally {
        settleInitialRequest();
      }
      if (this.closed) {
        await Promise.resolve();
        await this.cleanupLateSession();
        throw new Error("WHEP client is closed");
      }
      try {
        if (result.response.status === 201) {
          await this.acceptAnswer(result);
        } else if (result.response.status === 406) {
          await this.acceptCounterOffer(result);
        } else {
          throw new WHEPHTTPError(
            "WHEP endpoint rejected playback",
            result.response,
          );
        }
      } catch (error) {
        await discardResponse(result.response);
        throw error;
      }
      this.scheduleCandidatePatch(0);
      this.scheduleCredentialRestart();
    } catch (error) {
      await this.close();
      throw error;
    }
  }

  restart() {
    try {
      this.requireOpen();
      if (this.errorReported) {
        throw new Error("WHEP session has failed");
      }
    } catch (error) {
      return Promise.reject(asError(error));
    }
    if (!this.restartPromise) {
      this.restartPromise = this.performRestart().finally(() => {
        this.restartPromise = null;
      });
    }
    return this.restartPromise;
  }

  sessionResource() {
    return this.sessionURL?.toString() ?? null;
  }

  sessionHeader(name: string) {
    return this.sessionHeaders.get(name);
  }

  close() {
    if (!this.closePromise) {
      this.closePromise = this.performClose();
    }
    return this.closePromise;
  }

  private async performClose() {
    const startedAt = Date.now();
    this.closed = true;
    this.abort.abort();
    if (this.patchTimer) {
      clearTimeout(this.patchTimer);
      this.patchTimer = null;
    }
    this.clearCredentialRestart();
    let credentialRefreshFailed = false;
    let result: WHEPCloseResult;
    const signal = AbortSignal.timeout(this.closeTimeoutMs);
    let signalingSettled = true;
    try {
      if (this.initialRequest) {
        await waitForAbortable(this.initialRequest, signal);
      }
      await waitForAbortable(this.patchQueue, signal);
    } catch {
      signalingSettled = false;
    }
    const session = this.sessionURL;
    if (!signalingSettled) {
      result = closeResult("timed-out", startedAt, false);
    } else if (!session) {
      result = closeResult("not-established", startedAt, false);
    } else {
      if (this.refreshCredentials) {
        try {
          await this.ensureFreshCredentialsForClose(signal);
        } catch {
          credentialRefreshFailed = true;
        }
      }
      const deletion = await this.deleteRemoteSession(session, signal);
      result = closeResult(
        deletion.outcome,
        startedAt,
        credentialRefreshFailed,
        deletion.status,
      );
    }
    this.peer.close();
    try {
      this.onClose?.(result);
    } catch {
      // Observer failures cannot prevent local teardown.
    }
    return result;
  }

  private beginInitialRequest() {
    if (this.initialRequest) {
      throw new Error("WHEP initial request is already active");
    }
    let resolve = () => {};
    const pending = new Promise<void>((settled) => {
      resolve = settled;
    });
    this.initialRequest = pending;
    let completed = false;
    return () => {
      if (completed) {
        return;
      }
      completed = true;
      if (this.initialRequest === pending) {
        this.initialRequest = null;
      }
      resolve();
    };
  }

  private async cleanupLateSession() {
    const session = this.sessionURL;
    if (!session) {
      return;
    }
    const deletion = await this.deleteRemoteSession(
      session,
      AbortSignal.timeout(this.closeTimeoutMs),
    );
    if (retryableRemoteDelete(deletion)) {
      await this.deleteRemoteSession(
        session,
        AbortSignal.timeout(this.closeTimeoutMs),
      );
    }
  }

  private deleteRemoteSession(session: URL, signal: AbortSignal) {
    if (!this.remoteDeletePromise) {
      const deletion = this.performRemoteDelete(session, signal);
      this.remoteDeletePromise = deletion;
      void deletion.then((result) => {
        if (
          retryableRemoteDelete(result) &&
          this.remoteDeletePromise === deletion
        ) {
          this.remoteDeletePromise = null;
        }
      });
    }
    return this.remoteDeletePromise;
  }

  private async performRemoteDelete(
    session: URL,
    signal: AbortSignal,
  ): Promise<RemoteDeleteResult> {
    try {
      signal.throwIfAborted();
      const response = await this.fetch(session, {
        cache: "no-store",
        credentials: "omit",
        headers: authorizationHeaders(this.authorization),
        keepalive: true,
        method: "DELETE",
        redirect: "error",
        signal,
      });
      const outcome =
        response.status === 404 || response.status === 410
          ? "already-absent"
          : response.ok
            ? "deleted"
            : "http-error";
      await discardResponse(response).catch(() => {});
      return { outcome, status: response.status };
    } catch {
      return { outcome: signal.aborted ? "timed-out" : "request-error" };
    }
  }

  private async requestEndpoint(sdp: string) {
    return this.requestWithRedirects(this.endpoint, {
      body: addRTCPMuxOnly(sdp),
      cache: "no-store",
      credentials: "omit",
      headers: {
        Accept: "application/sdp",
        ...authorizationHeaders(this.authorization),
        "Content-Type": "application/sdp",
      },
      method: "POST",
      signal: this.abort.signal,
    });
  }

  private async acceptAnswer(result: ResponseWithURL) {
    requireMediaType(result.response, "application/sdp");
    this.etag = requireETag(
      result.response.headers.get("etag"),
      this.allowLegacyWildcardETag,
    );
    this.sessionHeaders = new Headers(result.response.headers);
    const answer = await this.readResponse(result.response);
    await this.peer.setRemoteDescription({ type: "answer", sdp: answer });
  }

  private async acceptCounterOffer(result: ResponseWithURL) {
    requireMediaType(result.response, "application/sdp");
    const deadline = counterOfferDeadline(
      result.response.headers.get("content-type"),
    );
    const sessionURL = this.requireSessionURL();
    const counterOffer = await this.readResponse(result.response);
    await this.peer.setLocalDescription({ type: "rollback" });
    this.replaceGeneration();
    await this.peer.setRemoteDescription({ type: "offer", sdp: counterOffer });
    const answer = await this.peer.createAnswer();
    await this.peer.setLocalDescription(answer);
    await this.waitForGathering(this.generation);
    const local = requireLocalDescription(this.peer);
    requireFreshCounterOffer(deadline);
    const response = await this.request(sessionURL, {
      body: addRTCPMuxOnly(local.sdp),
      cache: "no-store",
      credentials: "omit",
      headers: {
        ...authorizationHeaders(this.authorization),
        "Content-Type": "application/sdp",
      },
      method: "PATCH",
      redirect: "error",
      signal: this.abort.signal,
    });
    if (response.status !== 204) {
      const error = new WHEPHTTPError(
        "WHEP counter-offer answer failed",
        response,
      );
      await discardResponse(response);
      throw error;
    }
    await discardResponse(response);
    const etag = result.response.headers.get("etag");
    this.etag = etag ? requireETag(etag, this.allowLegacyWildcardETag) : null;
    this.sessionHeaders = new Headers(result.response.headers);
    this.generation.candidates = [];
  }

  private async performRestart() {
    if (!this.sessionURL || !this.etag) {
      throw new Error("WHEP endpoint did not enable ICE restart");
    }
    this.restartPending = true;
    this.cancelCandidatePatch();
    let localOfferApplied = false;
    try {
      await this.patchQueue;
      await this.ensureFreshCredentials(this.abort.signal, true);
      const generation = this.replaceGeneration();
      const offer = await this.peer.createOffer({ iceRestart: true });
      await this.peer.setLocalDescription(offer);
      localOfferApplied = true;
      await this.waitForGathering(generation);
      if (generation !== this.generation) {
        throw new Error("WHEP ICE restart was superseded");
      }
      const local = requireLocalDescription(this.peer);
      const fragment = createICEFragment(
        local.sdp,
        generation.candidates,
        true,
      );
      const response = await this.request(this.sessionURL, {
        body: fragment,
        cache: "no-store",
        credentials: "omit",
        headers: {
          Accept: "application/trickle-ice-sdpfrag",
          ...authorizationHeaders(this.authorization),
          "Content-Type": "application/trickle-ice-sdpfrag",
          "If-Match": "*",
        },
        method: "PATCH",
        redirect: "error",
        signal: this.abort.signal,
      });
      if (response.status !== 200) {
        const error = new WHEPHTTPError("WHEP ICE restart failed", response);
        await discardResponse(response);
        throw error;
      }
      requireMediaType(response, "application/trickle-ice-sdpfrag");
      const etag = requireETag(response.headers.get("etag"), false);
      const remoteFragment = await this.readResponse(response);
      const remote = this.peer.remoteDescription;
      if (!remote?.sdp) {
        throw new Error("WHEP remote description is unavailable");
      }
      const answer = replaceICEGeneration(remote.sdp, remoteFragment);
      await this.peer.setRemoteDescription({ type: "answer", sdp: answer });
      this.etag = etag;
      generation.embedded = candidateSet(local.sdp);
      generation.candidates = generation.candidates.filter(
        (candidate) => !generation.embedded.has(candidateKey(candidate)),
      );
      localOfferApplied = false;
    } catch (err) {
      if (localOfferApplied && this.peer.signalingState !== "stable") {
        await this.peer.setLocalDescription({ type: "rollback" });
      }
      throw err;
    } finally {
      this.restartPending = false;
    }
    this.scheduleCandidatePatch(0);
    this.scheduleCredentialRestart();
  }

  private handleLocalCandidate(event: RTCPeerConnectionIceEvent) {
    if (this.closed) {
      return;
    }
    const generation = this.generation;
    if (!event.candidate) {
      generation.complete = true;
      for (const resolve of generation.waiters.splice(0)) {
        resolve();
      }
      this.scheduleCandidatePatch(0);
      return;
    }
    const candidate = event.candidate.toJSON();
    if (generation.embedded.has(candidateKey(candidate))) {
      return;
    }
    if (generation.count >= maxCandidatesPerGeneration) {
      this.reportError(new Error("WHEP emitted too many local ICE candidates"));
      return;
    }
    generation.count += 1;
    generation.candidates.push(candidate);
    this.scheduleCandidatePatch(patchBatchDelayMs);
  }

  private scheduleCandidatePatch(delay: number) {
    if (
      this.closed ||
      this.restartPending ||
      !this.sessionURL ||
      !this.etag ||
      (this.generation.candidates.length === 0 &&
        (!this.generation.complete || this.generation.completionSent)) ||
      this.patchTimer
    ) {
      return;
    }
    this.patchTimer = setTimeout(() => {
      this.patchTimer = null;
      this.enqueuePatch(() => this.flushCandidates(this.generation));
    }, delay);
  }

  private cancelCandidatePatch() {
    if (this.patchTimer) {
      clearTimeout(this.patchTimer);
      this.patchTimer = null;
    }
  }

  private enqueuePatch(operation: () => Promise<void>) {
    this.patchQueue = this.patchQueue
      .then(operation, operation)
      .catch((err) => {
        if (!this.closed) {
          this.reportError(asError(err));
        }
      });
  }

  private async flushCandidates(generation: CandidateGeneration) {
    if (
      this.closed ||
      this.restartPending ||
      generation !== this.generation ||
      !this.sessionURL ||
      !this.etag
    ) {
      return;
    }
    const candidates = generation.candidates.splice(0);
    const sendCompletion = generation.complete && !generation.completionSent;
    if (candidates.length === 0 && !sendCompletion) {
      return;
    }
    await this.ensureFreshCredentials(this.abort.signal, false);
    const fragment = createICEFragment(
      requireLocalDescription(this.peer).sdp,
      candidates,
      sendCompletion,
    );
    const response = await this.request(this.sessionURL, {
      body: fragment,
      cache: "no-store",
      credentials: "omit",
      headers: {
        ...authorizationHeaders(this.authorization),
        "Content-Type": "application/trickle-ice-sdpfrag",
        "If-Match": this.etag,
      },
      method: "PATCH",
      redirect: "error",
      signal: this.abort.signal,
    });
    if (response.status !== 204) {
      const error = new WHEPHTTPError("WHEP candidate update failed", response);
      await discardResponse(response);
      throw error;
    }
    await discardResponse(response);
    if (sendCompletion) {
      generation.completionSent = true;
    }
    this.scheduleCandidatePatch(0);
  }

  private async waitForGathering(generation: CandidateGeneration) {
    if (generation.complete) {
      return;
    }
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        cleanup();
        reject(new Error("WHEP ICE gathering timed out"));
      }, gatherTimeoutMs);
      const onAbort = () => {
        cleanup();
        reject(new Error("WHEP negotiation was cancelled"));
      };
      const onComplete = () => {
        cleanup();
        resolve();
      };
      const cleanup = () => {
        clearTimeout(timeout);
        this.abort.signal.removeEventListener("abort", onAbort);
        const index = generation.waiters.indexOf(onComplete);
        if (index >= 0) {
          generation.waiters.splice(index, 1);
        }
      };
      generation.waiters.push(onComplete);
      this.abort.signal.addEventListener("abort", onAbort, { once: true });
    });
  }

  private replaceGeneration() {
    this.generation = newGeneration(this.generation.generation + 1);
    return this.generation;
  }

  private ensureFreshCredentials(signal: AbortSignal, force: boolean) {
    if (!this.refreshCredentials) {
      return Promise.resolve();
    }
    if (
      !force &&
      this.credentialExpiresAt !== null &&
      this.credentialExpiresAt - Date.now() > credentialRefreshLeadMs
    ) {
      return Promise.resolve();
    }
    if (!this.credentialRefreshPromise) {
      const refresh = this.refreshCredentials(signal)
        .then((credentials) => this.applyCredentials(credentials))
        .finally(() => {
          if (this.credentialRefreshPromise === refresh) {
            this.credentialRefreshPromise = null;
          }
        });
      this.credentialRefreshPromise = refresh;
    }
    return this.credentialRefreshPromise;
  }

  private async ensureFreshCredentialsForClose(signal: AbortSignal) {
    if (
      !this.refreshCredentials ||
      (this.credentialExpiresAt !== null &&
        this.credentialExpiresAt - Date.now() > credentialRefreshLeadMs)
    ) {
      return;
    }
    const pending = this.credentialRefreshPromise;
    if (pending) {
      try {
        await waitForAbortable(pending, signal);
        return;
      } catch {
        if (signal.aborted) {
          throw signal.reason;
        }
      }
    }
    const credentials = await waitForAbortable(
      this.refreshCredentials(signal),
      signal,
    );
    this.applyCredentials(credentials);
  }

  private applyCredentials(credentials: WHEPCredentialRefresh) {
    const endpoint = secureURL(credentials.endpoint, this.allowInsecureHTTP);
    if (credentialTarget(endpoint) !== credentialTarget(this.endpoint)) {
      throw new Error("WHEP refreshed credentials changed the active target");
    }
    const authorization = normalizeAuthorization(credentials.authorization);
    const expiresAt = parseCredentialExpiration(credentials.expiresAt);
    if (expiresAt === null || expiresAt <= Date.now()) {
      throw new Error("WHEP refreshed credentials are expired");
    }
    const iceExpiresAt = parseCredentialExpiration(credentials.iceExpiresAt);
    if (iceExpiresAt === null || iceExpiresAt <= Date.now()) {
      throw new Error("WHEP refreshed ICE credentials are expired");
    }
    const configuration = this.peer.getConfiguration();
    this.peer.setConfiguration({
      ...configuration,
      iceServers: credentials.iceServers,
    });
    this.authorization = authorization;
    this.credentialExpiresAt = expiresAt;
    this.iceCredentialExpiresAt = iceExpiresAt;
    this.endpoint = endpoint;
    if (this.sessionURL) {
      copyEdgeCredential(endpoint, this.sessionURL);
    }
  }

  private scheduleCredentialRestart(retryDelayMs?: number) {
    this.clearCredentialRestart();
    if (this.closed || this.iceCredentialExpiresAt === null) {
      return;
    }
    const remaining = Math.max(0, this.iceCredentialExpiresAt - Date.now());
    const delay = Math.min(
      retryDelayMs ?? Math.max(0, remaining - credentialRefreshLeadMs),
      remaining,
      maximumTimerDelayMs,
    );
    this.iceRestartTimer = setTimeout(() => {
      this.iceRestartTimer = null;
      if (this.closed) {
        return;
      }
      if (
        this.iceCredentialExpiresAt !== null &&
        this.iceCredentialExpiresAt - Date.now() > credentialRefreshLeadMs
      ) {
        this.scheduleCredentialRestart();
        return;
      }
      void this.restart()
        .then(() => {
          this.credentialRefreshRetryMs = credentialRefreshRetryInitialMs;
        })
        .catch((error) => {
          if (this.closed || this.iceCredentialExpiresAt === null) {
            return;
          }
          const retryRemaining = this.iceCredentialExpiresAt - Date.now();
          if (retryRemaining <= 0) {
            this.reportError(
              new Error("WHEP ICE credentials expired before renewal", {
                cause: asError(error),
              }),
            );
            return;
          }
          const retryDelay = Math.min(
            this.credentialRefreshRetryMs,
            retryRemaining,
          );
          this.credentialRefreshRetryMs = Math.min(
            this.credentialRefreshRetryMs * 2,
            credentialRefreshRetryMaximumMs,
          );
          this.scheduleCredentialRestart(retryDelay);
        });
    }, delay);
  }

  private clearCredentialRestart() {
    if (this.iceRestartTimer) {
      clearTimeout(this.iceRestartTimer);
      this.iceRestartTimer = null;
    }
  }

  private async requestWithRedirects(url: URL, init: RequestInit) {
    let current = url;
    for (let redirects = 0; ; redirects += 1) {
      const response = await this.request(current, {
        ...init,
        redirect: "manual",
      });
      if (![307, 308].includes(response.status)) {
        return { response, url: current };
      }
      if (redirects >= maxRedirects) {
        await discardResponse(response);
        throw new Error("WHEP endpoint exceeded the redirect limit");
      }
      const location = response.headers.get("location");
      await discardResponse(response);
      if (!location) {
        throw new Error("WHEP redirect omitted Location");
      }
      const next = secureURL(
        new URL(location, current).toString(),
        this.allowInsecureHTTP,
      );
      if (!this.trustedRedirectOrigins.has(next.origin)) {
        throw new Error("WHEP redirect changed to an untrusted origin");
      }
      if (next.origin === current.origin) {
        copyEdgeCredential(current, next);
      }
      current = next;
    }
  }

  private async request(input: URL, init: RequestInit) {
    const parent = init.signal;
    const controller = new AbortController();
    let timedOut = false;
    const abort = () => controller.abort(parent?.reason);
    if (parent?.aborted) {
      abort();
    } else {
      parent?.addEventListener("abort", abort, { once: true });
    }
    const timer = setTimeout(() => {
      timedOut = true;
      controller.abort(new Error("WHEP request timed out"));
    }, this.requestTimeoutMs);
    try {
      return await this.fetch(input, { ...init, signal: controller.signal });
    } catch (error) {
      if (timedOut) {
        throw new Error("WHEP request timed out", { cause: error });
      }
      throw error;
    } finally {
      clearTimeout(timer);
      parent?.removeEventListener("abort", abort);
    }
  }

  private readResponse(response: Response) {
    return readBoundedResponse(
      response,
      this.abort.signal,
      this.requestTimeoutMs,
    );
  }

  private captureSessionURL(result: ResponseWithURL) {
    this.sessionURL = sessionURL(
      result.url,
      result.response.headers.get("location"),
      this.allowInsecureHTTP,
    );
  }

  private requireSessionURL() {
    if (!this.sessionURL) {
      throw new Error("WHEP session Location is unavailable");
    }
    return this.sessionURL;
  }

  private requireOpen() {
    if (this.closed) {
      throw new Error("WHEP client is closed");
    }
  }

  private reportError(error: Error) {
    if (this.errorReported) {
      return;
    }
    this.errorReported = true;
    this.clearCredentialRestart();
    try {
      this.onError(error);
    } catch {
      // Observer failures cannot alter the signaling state machine.
    }
  }
}

function waitForAbortable<Result>(
  operation: Promise<Result>,
  signal: AbortSignal,
) {
  if (signal.aborted) {
    return Promise.reject<Result>(signal.reason);
  }
  return new Promise<Result>((resolve, reject) => {
    const aborted = () => {
      cleanup();
      reject(signal.reason);
    };
    const cleanup = () => signal.removeEventListener("abort", aborted);
    signal.addEventListener("abort", aborted, { once: true });
    operation.then(
      (result) => {
        cleanup();
        resolve(result);
      },
      (error) => {
        cleanup();
        reject(error);
      },
    );
  });
}

export function addRTCPMuxOnly(raw: string) {
  const sections = splitSDP(raw);
  for (let index = 1; index < sections.length; index += 1) {
    const lines = sections[index];
    if (mediaPort(lines[0]) === 0 || !hasLine(lines, "a=rtcp-mux")) {
      continue;
    }
    if (!hasLine(lines, "a=rtcp-mux-only")) {
      const rtcpMux = lines.findIndex((line) => line === "a=rtcp-mux");
      lines.splice(rtcpMux + 1, 0, "a=rtcp-mux-only");
    }
    if (!lines.some((line) => line.startsWith("a=msid:"))) {
      lines.push("a=msid:rstream-whep rstream-video");
    }
  }
  return joinSDP(sections);
}

export function createICEFragment(
  raw: string,
  candidates: RTCIceCandidateInit[],
  complete: boolean,
) {
  const sections = splitSDP(raw);
  const session = sections[0];
  const bundle = valueLine(session, "a=group:BUNDLE ");
  const taggedMID = bundle?.split(/\s+/)[0];
  const media = sections.slice(1).find((lines) => {
    return valueLine(lines, "a=mid:") === taggedMID;
  });
  if (!bundle || !taggedMID || !media) {
    throw new Error("WHEP SDP does not contain a tagged BUNDLE media section");
  }
  const ufrag = attribute(session, media, "ice-ufrag");
  const pwd = attribute(session, media, "ice-pwd");
  if (!ufrag || !pwd) {
    throw new Error("WHEP SDP does not contain ICE credentials");
  }
  const fragment = [
    ...copyAttributes(session, [
      "a=ice-lite",
      "a=ice-options:",
      "a=ice-pacing:",
    ]),
    `a=group:BUNDLE ${bundle}`,
    media[0],
    `a=mid:${taggedMID}`,
    `a=ice-ufrag:${ufrag}`,
    `a=ice-pwd:${pwd}`,
    ...candidates.map((candidate) => normalizeCandidate(candidate.candidate)),
  ];
  if (complete) {
    fragment.push("a=end-of-candidates");
  }
  return `${fragment.join("\r\n")}\r\n`;
}

export function replaceICEGeneration(raw: string, fragment: string) {
  const sections = splitSDP(raw);
  const update = parseICEFragment(fragment);
  const session = sections[0];
  replaceSessionICEAttributes(session, update.sessionAttributes);
  for (let index = 1; index < sections.length; index += 1) {
    const lines = sections[index];
    replaceAttribute(lines, "a=ice-ufrag:", update.ufrag);
    replaceAttribute(lines, "a=ice-pwd:", update.pwd);
    removeLines(lines, [
      "a=candidate:",
      "a=end-of-candidates",
      "a=remote-candidates:",
    ]);
    if (valueLine(lines, "a=mid:") === update.mid) {
      const insertAt = Math.max(
        lines.findIndex((line) => line.startsWith("a=mid:")) + 1,
        1,
      );
      lines.splice(insertAt, 0, ...update.candidates);
      if (update.complete) {
        lines.splice(
          insertAt + update.candidates.length,
          0,
          "a=end-of-candidates",
        );
      }
    }
  }
  return joinSDP(sections);
}

export function sessionURL(
  endpoint: URL,
  location: string | null,
  allowInsecureHTTP = false,
) {
  if (!location) {
    throw new Error("WHEP endpoint omitted the session Location");
  }
  const session = secureURL(
    new URL(location, endpoint).toString(),
    allowInsecureHTTP,
  );
  if (session.origin !== endpoint.origin) {
    throw new Error("WHEP session Location changed origin");
  }
  copyEdgeCredential(endpoint, session);
  return session;
}

function credentialTarget(input: URL) {
  const target = new URL(input);
  target.searchParams.delete("rstream.token");
  target.searchParams.sort();
  return target.toString();
}

function copyEdgeCredential(source: URL, destination: URL) {
  destination.searchParams.delete("rstream.token");
  const token = source.searchParams.get("rstream.token");
  if (token) {
    destination.searchParams.set("rstream.token", token);
  }
}

function parseCredentialExpiration(raw: string | undefined) {
  if (raw === undefined) {
    return null;
  }
  const expiresAt = Date.parse(raw);
  if (!Number.isFinite(expiresAt)) {
    throw new Error("WHEP credential expiration is invalid");
  }
  return expiresAt;
}

function parseRetryAfter(raw: string | null) {
  const value = raw?.trim() ?? "";
  if (!value) {
    return null;
  }
  if (/^\d+$/.test(value)) {
    const seconds = Number(value);
    if (!Number.isSafeInteger(seconds)) {
      return maximumTimerDelayMs;
    }
    return Math.min(seconds * 1000, maximumTimerDelayMs);
  }
  const date = parseHTTPDate(value);
  if (date === null) {
    return null;
  }
  return Math.min(Math.max(0, date - Date.now()), maximumTimerDelayMs);
}

function parseHTTPDate(raw: string) {
  const imfFixdate = raw.match(
    /^(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), (\d{2}) ([A-Z][a-z]{2}) (\d{4}) (\d{2}):(\d{2}):(\d{2}) GMT$/,
  );
  if (imfFixdate) {
    return httpDateFromFields(
      imfFixdate[3],
      imfFixdate[2],
      imfFixdate[1],
      imfFixdate[4],
      imfFixdate[5],
      imfFixdate[6],
    );
  }
  const rfc850 = raw.match(
    /^(?:Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday), (\d{2})-([A-Z][a-z]{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2}) GMT$/,
  );
  if (rfc850) {
    const currentYear = new Date(Date.now()).getUTCFullYear();
    const shortYear = Number(rfc850[3]);
    let year = Math.floor(currentYear / 100) * 100 + shortYear;
    if (year > currentYear + 50) {
      year -= 100;
    }
    return httpDateFromFields(
      String(year),
      rfc850[2],
      rfc850[1],
      rfc850[4],
      rfc850[5],
      rfc850[6],
    );
  }
  const asctime = raw.match(
    /^(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun) ([A-Z][a-z]{2}) {1,2}(\d{1,2}) (\d{2}):(\d{2}):(\d{2}) (\d{4})$/,
  );
  if (!asctime) {
    return null;
  }
  return httpDateFromFields(
    asctime[6],
    asctime[1],
    asctime[2],
    asctime[3],
    asctime[4],
    asctime[5],
  );
}

function httpDateFromFields(
  rawYear: string,
  rawMonth: string,
  rawDay: string,
  rawHour: string,
  rawMinute: string,
  rawSecond: string,
) {
  const year = Number(rawYear);
  const month = httpMonths.get(rawMonth);
  const day = Number(rawDay);
  const hour = Number(rawHour);
  const minute = Number(rawMinute);
  const second = Number(rawSecond);
  if (
    month === undefined ||
    !Number.isSafeInteger(year) ||
    year < 0 ||
    year > 9999 ||
    !Number.isSafeInteger(day) ||
    !Number.isSafeInteger(hour) ||
    !Number.isSafeInteger(minute) ||
    !Number.isSafeInteger(second) ||
    day < 1 ||
    hour < 0 ||
    hour > 23 ||
    minute < 0 ||
    minute > 59 ||
    second < 0 ||
    second > 59
  ) {
    return null;
  }
  const result = Date.UTC(year, month, day, hour, minute, second);
  const date = new Date(result);
  if (
    date.getUTCFullYear() !== year ||
    date.getUTCMonth() !== month ||
    date.getUTCDate() !== day
  ) {
    return null;
  }
  return result;
}

function closeResult(
  outcome: WHEPCloseResult["outcome"],
  startedAt: number,
  credentialRefreshFailed: boolean,
  status?: number,
): WHEPCloseResult {
  return {
    credentialRefreshFailed,
    durationMilliseconds: Math.max(0, Date.now() - startedAt),
    outcome,
    ...(status === undefined ? {} : { status }),
  };
}

function retryableRemoteDelete(result: RemoteDeleteResult) {
  return (
    result.outcome === "request-error" ||
    result.outcome === "timed-out" ||
    (result.outcome === "http-error" &&
      result.status !== undefined &&
      ([408, 409, 425, 429].includes(result.status) || result.status >= 500))
  );
}

export function requireETag(raw: string | null, allowLegacyWildcard: boolean) {
  const value = raw?.trim() ?? "";
  if (allowLegacyWildcard && value === "*") {
    return value;
  }
  if (!/^"[\x21\x23-\x7e\x80-\xff]*"$/.test(value)) {
    throw new Error("WHEP endpoint did not return a strong ETag");
  }
  return value;
}

function parseICEFragment(raw: string) {
  const lines = normalizeSDP(raw).split("\r\n").filter(Boolean);
  const mediaIndex = lines.findIndex((line) => line.startsWith("m="));
  if (mediaIndex < 0) {
    throw new Error("WHEP ICE response has no media section");
  }
  const session = lines.slice(0, mediaIndex);
  const media = lines.slice(mediaIndex);
  const ufrag = attribute(session, media, "ice-ufrag");
  const pwd = attribute(session, media, "ice-pwd");
  const mid = valueLine(media, "a=mid:");
  if (!ufrag || !pwd || !mid) {
    throw new Error("WHEP ICE response is incomplete");
  }
  const candidates = media.filter((line) => line.startsWith("a=candidate:"));
  if (candidates.length > maxCandidatesPerGeneration) {
    throw new Error("WHEP ICE response contains too many candidates");
  }
  return {
    candidates,
    complete: hasLine(media, "a=end-of-candidates"),
    mid,
    pwd,
    sessionAttributes: copyAttributes(session, [
      "a=ice-lite",
      "a=ice-options:",
      "a=ice-pacing:",
    ]),
    ufrag,
  };
}

function replaceSessionICEAttributes(lines: string[], attributes: string[]) {
  removeLines(lines, ["a=ice-lite", "a=ice-options:", "a=ice-pacing:"]);
  lines.push(...attributes);
}

function replaceAttribute(lines: string[], prefix: string, value: string) {
  let replaced = false;
  for (let index = 0; index < lines.length; index += 1) {
    if (lines[index].startsWith(prefix)) {
      lines[index] = `${prefix}${value}`;
      replaced = true;
    }
  }
  if (!replaced) {
    lines.push(`${prefix}${value}`);
  }
}

function removeLines(lines: string[], prefixes: string[]) {
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    if (prefixes.some((prefix) => lines[index].startsWith(prefix))) {
      lines.splice(index, 1);
    }
  }
}

function copyAttributes(lines: string[], prefixes: string[]) {
  return lines.filter((line) =>
    prefixes.some((prefix) => line.startsWith(prefix)),
  );
}

function attribute(session: string[], media: string[], name: string) {
  return valueLine(media, `a=${name}:`) ?? valueLine(session, `a=${name}:`);
}

function valueLine(lines: string[], prefix: string) {
  const line = lines.find((value) => value.startsWith(prefix));
  return line?.slice(prefix.length).trim();
}

function mediaPort(line: string) {
  const port = Number(line.split(/\s+/)[1]);
  return Number.isFinite(port) ? port : -1;
}

function hasLine(lines: string[], value: string) {
  return lines.includes(value);
}

function splitSDP(raw: string) {
  const lines = normalizeSDP(raw).split("\r\n").filter(Boolean);
  if (lines[0] !== "v=0") {
    throw new Error("WHEP SDP is invalid");
  }
  const sections: string[][] = [[]];
  for (const line of lines) {
    if (line.startsWith("m=")) {
      sections.push([]);
    }
    sections[sections.length - 1].push(line);
  }
  return sections;
}

function joinSDP(sections: string[][]) {
  return `${sections.flat().join("\r\n")}\r\n`;
}

function normalizeSDP(raw: string) {
  return raw.replace(/\r?\n/g, "\r\n").replace(/(?:\r\n)+$/, "");
}

function normalizeCandidate(raw: string | undefined) {
  const candidate = raw?.trim() ?? "";
  if (!candidate.startsWith("candidate:")) {
    throw new Error("WHEP local ICE candidate is invalid");
  }
  return `a=${candidate}`;
}

function candidateSet(raw: string) {
  const set = new Set<string>();
  for (const line of normalizeSDP(raw).split("\r\n")) {
    if (line.startsWith("a=candidate:")) {
      set.add(line.slice(2));
    }
  }
  return set;
}

function candidateKey(candidate: RTCIceCandidateInit) {
  return candidate.candidate?.trim() ?? "";
}

function captureEmbeddedCandidates(
  generation: CandidateGeneration,
  raw: string,
) {
  const embedded = candidateSet(raw);
  const queued = generation.candidates.filter(
    (candidate) => !embedded.has(candidateKey(candidate)),
  );
  const unique = new Set(embedded);
  for (const candidate of queued) {
    unique.add(candidateKey(candidate));
  }
  if (unique.size > maxCandidatesPerGeneration) {
    throw new Error("WHEP emitted too many local ICE candidates");
  }
  generation.embedded = embedded;
  generation.candidates = queued;
  generation.count = unique.size;
}

function newGeneration(generation: number): CandidateGeneration {
  return {
    candidates: [],
    complete: false,
    completionSent: false,
    count: 0,
    embedded: new Set(),
    generation,
    waiters: [],
  };
}

function secureURL(raw: string, allowInsecureHTTP = false) {
  const url = new URL(raw);
  const secure = url.protocol === "https:";
  const permittedHTTP =
    url.protocol === "http:" &&
    (isLoopbackHost(url.hostname) || allowInsecureHTTP);
  if (!secure && !permittedHTTP) {
    throw new Error("WHEP URLs must use HTTPS");
  }
  if (url.username || url.password || url.hash) {
    throw new Error("WHEP URL contains forbidden credentials or fragment");
  }
  const edgeCredentials = url.searchParams.getAll("rstream.token");
  if (
    edgeCredentials.length > 1 ||
    (edgeCredentials.length === 1 && !edgeCredentials[0].trim())
  ) {
    throw new Error("WHEP edge credential is invalid");
  }
  return url;
}

function normalizeAuthorization(raw: string) {
  const value = raw.trim();
  if (value.length > 8192 || /[\r\n\0]/.test(value)) {
    throw new Error("WHEP authorization is invalid");
  }
  return value;
}

function isLoopbackHost(hostname: string) {
  const normalized = hostname.toLowerCase().replace(/^\[(.*)\]$/, "$1");
  return (
    normalized === "localhost" ||
    normalized === "::1" ||
    normalized.startsWith("127.")
  );
}

function positiveDuration(value: number, name: string) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${name} must be a positive integer`);
  }
  return value;
}

function defaultPeerFactory(configuration: RTCConfiguration) {
  return new RTCPeerConnection(configuration);
}

function requireLocalDescription(peer: RTCPeerConnection) {
  if (!peer.localDescription?.sdp) {
    throw new Error("WHEP local description is unavailable");
  }
  return peer.localDescription;
}

function requireMediaType(response: Response, expected: string) {
  const mediaType = response.headers
    .get("content-type")
    ?.split(";", 1)[0]
    ?.trim()
    .toLowerCase();
  if (mediaType !== expected) {
    throw new Error(`WHEP endpoint returned ${mediaType || "no Content-Type"}`);
  }
}

function counterOfferDeadline(raw: string | null) {
  if (!raw || !/(?:^|;)\s*valid-until\s*=/i.test(raw)) {
    return Date.now() + 30_000;
  }
  const matches = [
    ...raw.matchAll(/(?:^|;)\s*valid-until\s*=\s*(?:"([^"]+)"|([^;\s]+))/gi),
  ];
  if (matches.length !== 1) {
    throw new Error("WHEP counter-offer valid-until is invalid");
  }
  const deadline = Date.parse(matches[0][1] ?? matches[0][2] ?? "");
  if (!Number.isFinite(deadline)) {
    throw new Error("WHEP counter-offer valid-until is invalid");
  }
  requireFreshCounterOffer(deadline);
  return deadline;
}

function requireFreshCounterOffer(deadline: number) {
  if (deadline <= Date.now()) {
    throw new Error("WHEP counter-offer expired");
  }
}

async function readBoundedResponse(
  response: Response,
  signal: AbortSignal,
  timeoutMs: number,
) {
  const contentLength = Number(response.headers.get("content-length"));
  if (Number.isFinite(contentLength) && contentLength > maxResponseBytes) {
    await discardResponse(response);
    throw new Error(`WHEP response exceeds ${maxResponseBytes} bytes`);
  }
  if (!response.body) {
    throw new Error("WHEP endpoint returned an empty response");
  }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let length = 0;
  let cancelled = false;
  let timedOut = false;
  const cancel = () => {
    cancelled = true;
    void reader.cancel(signal.reason).catch(() => {});
  };
  if (signal.aborted) {
    cancel();
  } else {
    signal.addEventListener("abort", cancel, { once: true });
  }
  const timer = setTimeout(() => {
    timedOut = true;
    void reader
      .cancel(new Error("WHEP response read timed out"))
      .catch(() => {});
  }, timeoutMs);
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (timedOut) {
        throw new Error("WHEP response read timed out");
      }
      if (cancelled || signal.aborted) {
        throw new Error("WHEP response read was cancelled");
      }
      if (done) {
        break;
      }
      length += value.byteLength;
      if (length > maxResponseBytes) {
        await reader.cancel();
        throw new Error(`WHEP response exceeds ${maxResponseBytes} bytes`);
      }
      chunks.push(value);
    }
  } catch (error) {
    if (timedOut) {
      throw new Error("WHEP response read timed out", { cause: error });
    }
    if (cancelled || signal.aborted) {
      throw new Error("WHEP response read was cancelled", { cause: error });
    }
    throw error;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", cancel);
  }
  const body = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    body.set(chunk, offset);
    offset += chunk.byteLength;
  }
  const value = new TextDecoder().decode(body);
  if (!value.trim()) {
    throw new Error("WHEP endpoint returned an empty response");
  }
  return value;
}

async function discardResponse(response: Response) {
  try {
    await response.body?.cancel();
  } catch {
    // Response cleanup must not hide the protocol result.
  }
}

function asError(value: unknown) {
  return value instanceof Error ? value : new Error("WHEP operation failed");
}
