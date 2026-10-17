# Synthetic retrieval fixture

Every person, event and place in this corpus is fictional. No text comes from an identity record or user conversation. Names in the examples are synthetic test inputs, not contributor credits or actual identity names.

The manifest freezes the corpus and cue bytes with SHA-256 hashes and declares the evaluation thresholds. Corpus IDs, cue targets, negative controls, languages and thresholds are retained unchanged. The tests verify parsing, exact hashes and evaluator behavior; a local unit test does not establish model or embedding performance. Run the documented optional evaluator with an explicitly selected embedding implementation to measure those results.
