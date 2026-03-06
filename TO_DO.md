### TO DO

- this crap in feedback: <|start|>functions.read_file<|channel|>commentary to=assistant code<|message|>{"path":"index.html","offset":1,"limit":2000}
- maybe read limit is too small? this crap in summary: <think>The read_file tool is consistently returning only the first ~100 lines before truncating at roughly the same point. This suggests either: 1. The file is genuinely that size (unlikely for a full HTML) 2. There's a bug with the tool
