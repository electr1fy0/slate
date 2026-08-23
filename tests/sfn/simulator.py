"""A minimal ASL interpreter sufficient for exercising statemachine/failover.asl.json offline.

Supports: Task (arn:aws:states:::lambda:invoke and arn:aws:states:::sns:publish),
Choice (BooleanEquals), Wait, Succeed, Fail, ResultPath insertion, Retry
(attempt-limited re-invocation) and Catch routing. Waits execute instantly.
"""


class ExecutionFailed(Exception):
    def __init__(self, error, cause):
        super().__init__(f"{error}: {cause}")
        self.error = error
        self.cause = cause


def resolve(path, data):
    cur = data
    for part in path.lstrip("$.").split("."):
        if not part:
            continue
        cur = cur[part]
    return cur


class Simulator:
    LAMBDA_RESOURCE = "arn:aws:states:::lambda:invoke"
    SNS_RESOURCE = "arn:aws:states:::sns:publish"

    def __init__(self, asl, lambdas, execution_name="simulated-execution"):
        self.asl = asl
        self.lambdas = lambdas
        self.execution_name = execution_name
        self.sns_publishes = []
        self.invocations = []
        self.context = {}
        self.history = []
        self.outcome = None
        self.failure = None

    def run(self, initial_input=None):
        self.context = dict(initial_input or {})
        self.history = []
        self.outcome = None
        self.failure = None
        state_name = self.asl["StartAt"]
        while True:
            state = self.asl["States"][state_name]
            self.history.append(state_name)
            kind = state["Type"]

            if kind == "Task":
                nxt = self._run_task(state)
                if nxt is None:
                    break
                state_name = nxt
            elif kind == "Choice":
                state_name = self._evaluate_choice(state)
            elif kind == "Wait":
                state_name = state["Next"]
            elif kind == "Fail":
                self.outcome = "failed"
                self.failure = {"error": state["Error"], "cause": state["Cause"]}
                break
            elif kind == "Succeed":
                self.outcome = "success"
                break
            else:
                raise NotImplementedError(f"state type {kind!r} not simulated")

            if state_name is None:
                break

        if self.outcome is None:
            self.outcome = "success"
        return self

    # ------------------------------------------------------------------ tasks

    def _run_task(self, state):
        try:
            result = self._invoke_with_retries(state)
        except ExecutionFailed as failure:
            for catch in state.get("Catch", []):
                if "States.ALL" in catch["ErrorEquals"] or failure.error in catch["ErrorEquals"]:
                    slot = catch["ResultPath"].lstrip("$.")
                    self.context[slot] = {"Error": failure.error, "Cause": failure.cause}
                    return catch["Next"]
            self.outcome = "aborted"
            self.failure = {"error": failure.error, "cause": failure.cause}
            return None

        slot = state.get("ResultPath", "$").lstrip("$.")
        if slot:
            self.context[slot] = result
        if state.get("End"):
            self.outcome = "success"
            return None
        return state["Next"]

    def _invoke_with_retries(self, state):
        policy = (state.get("Retry") or [None])[0]
        attempt = 0
        while True:
            try:
                return self._invoke(state)
            except ExecutionFailed:
                raise
            except Exception as exc:
                if policy and attempt < int(policy.get("MaxAttempts", 0)):
                    attempt += 1
                    continue
                raise ExecutionFailed("States.TaskFailed", str(exc)[:120]) from exc

    def _invoke(self, state):
        params = state.get("Parameters", {})
        resource = state["Resource"]
        if resource == self.LAMBDA_RESOURCE:
            fn = self.lambdas[params["FunctionName"]]
            payload = dict(params.get("Payload", {}))
            self.invocations.append({"function": params["FunctionName"], "payload": payload})
            result = fn(payload, None)
            # SFN wraps lambda output under Payload
            return {"Payload": result}
        if resource == self.SNS_RESOURCE:
            if "Message.$" in params:
                message = self._render(params["Message.$"], self.context)
            else:
                message = params.get("Message", "")
            entry = {"TopicArn": params["TopicArn"],
                     "Subject": params.get("Subject", ""),
                     "Message": message}
            self.sns_publishes.append(entry)
            return {}
        raise NotImplementedError(f"resource {resource!r} not simulated")

    # --------------------------------------------------------------- choices

    def _evaluate_choice(self, state):
        for choice in state["Choices"]:
            actual = bool(resolve(choice["Variable"], self.context))
            if actual is choice["BooleanEquals"]:
                return choice["Next"]
        return state["Default"]

    # ---------------------------------------------------------------- messages

    def _find_close(self, text, open_idx):
        depth = 0
        in_quote = False
        for i in range(open_idx, len(text)):
            ch = text[i]
            if ch == "'":
                in_quote = not in_quote
            elif not in_quote:
                if ch == "(":
                    depth += 1
                elif ch == ")":
                    depth -= 1
                    if depth == 0:
                        return i
        raise ValueError(f"unbalanced parens in {text!r}")

    def _split_top_level(self, s):
        parts = []
        buf = ""
        depth = 0
        in_quote = False
        for ch in s:
            if ch == "'":
                in_quote = not in_quote
                buf += ch
            elif in_quote:
                buf += ch
            elif ch == "(":
                depth += 1
                buf += ch
            elif ch == ")":
                depth -= 1
                buf += ch
            elif ch == "," and depth == 0:
                parts.append(buf)
                buf = ""
            else:
                buf += ch
        parts.append(buf)
        return [p.strip() for p in parts]

    def _eval_arg(self, arg):
        arg = arg.strip()
        if arg.startswith("'") and arg.endswith("'"):
            return arg[1:-1]
        if arg.startswith("States.JsonToString("):
            close = self._find_close(arg, arg.index("("))
            inner = arg[arg.index("(") + 1:close].strip()
            try:
                value = resolve(inner, self.context)
            except (KeyError, TypeError):
                # SFN renders JsonToString over a missing/null path as "null"
                value = None
            return json_dumps(value)
        if arg.startswith("$"):
            return str(resolve(arg, self.context))
        return arg

    def _render(self, template, ctx):
        rendered = template.replace("$$.Execution.Name", self.execution_name)
        token = "States.Format("
        while token in rendered:
            start = rendered.index(token)
            open_idx = start + len(token) - 1
            close_idx = self._find_close(rendered, open_idx)
            inner = rendered[open_idx + 1:close_idx]
            args = self._split_top_level(inner)
            out = self._eval_arg(args[0])
            for placeholder_count, arg in enumerate(args[1:]):
                out = out.replace("{}", str(self._eval_arg(arg)), 1)
            rendered = rendered[:start] + out + rendered[close_idx + 1:]
        return rendered


def json_dumps(obj):
    import json
    return json.dumps(obj, default=str)
