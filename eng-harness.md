ChatGPT



Pasted text(2).txt
Document
hreformat "

Thought for a couple of seconds
Here is a cleaner reformatted version of the pasted text:


Harness Design for Long-Running Application Development
Published: March 24, 2026
Author: Prithvi Rajasekaran, Anthropic Labs

Main idea
Harness design is critical for improving AI coding agents, especially for long-running software development tasks. Anthropic tested multi-agent workflows that help Claude produce better frontend designs and build more complete full-stack applications.

The core pattern was inspired by GANs:

Generator agent creates the output.

Evaluator agent reviews the output critically.

Planner agent expands short prompts into detailed product specs.

This structure helped Claude perform better than a single-agent approach, especially on complex coding tasks.

Why simple agent setups fail
Naive long-running coding agents often run into two problems.

1. Loss of coherence over time
   As the context window fills, agents can lose track of the task or start wrapping up too early. Earlier harnesses used context resets with structured handoff artifacts to solve this.

A reset gives the next agent a clean slate, while the handoff artifact preserves the important state.

2. Weak self-evaluation
   Agents are often too generous when reviewing their own work. They may confidently say something is good even when the result is mediocre or broken.

Separating the builder from the reviewer helps. A standalone evaluator can be tuned to be more skeptical and specific.

Frontend design experiment
Anthropic first tested this approach on frontend design.

The generator created frontend pages. The evaluator used Playwright MCP to inspect the live page and score it.

The evaluator graded designs on four criteria:

Design quality
Does the design feel coherent and intentional?

Originality
Does it avoid generic templates and common AI-generated patterns?

Craft
Are typography, spacing, colors, contrast, and layout executed well?

Functionality
Can users understand and use the interface?

The most important criteria were design quality and originality, because Claude already tended to do reasonably well on basic craft and functionality.

The loop usually ran for 5–15 iterations. Over time, the generator often produced more distinctive and creative designs.

Full-stack coding harness
Anthropic then applied the same pattern to full-stack application development.

The system used three agents:

Planner
Takes a short user prompt and expands it into a full product spec.

The planner focuses on:

product goals

user stories

high-level technical design

possible AI features

It avoids over-specifying low-level implementation details.

Generator
Builds the app from the spec.

In the earlier version, the generator worked in sprints, one feature at a time. Each sprint had a clear contract defining what “done” meant.

Evaluator
Uses Playwright MCP to test the running app like a real user.

It checks:

UI behavior

API endpoints

database state

feature completeness

bugs

usability

code quality

If a sprint fails, the evaluator gives detailed feedback to the generator.

Sprint contracts
Before each sprint, the generator and evaluator agree on a sprint contract.

The contract defines:

what will be built

how success will be tested

what behaviors must work

This keeps the work aligned with the product spec without forcing the planner to define every technical detail upfront.

Example: Retro game maker
Anthropic tested the harness on this prompt:

Create a 2D retro game maker with features including a level editor, sprite editor, entity behaviors, and a playable test mode.

They compared two approaches:

Harness type	Duration	Cost
Solo agent	20 minutes	$9
Full harness	6 hours	$200
The full harness was much more expensive, but the output was much better.

The solo app looked promising at first, but the actual gameplay was broken. Entities appeared, but player input did not work.

The full harness produced a more polished app with:

better layout

usable canvas

richer sprite editor

AI-assisted game creation

working playable mode

better adherence to the spec

It still had rough edges, but the core product worked.

Bugs caught by the evaluator
The evaluator found specific implementation issues, such as:

Contract criterion	Evaluator finding
Rectangle fill tool should fill an area	Tool only placed tiles at drag start/end points
User can delete placed entity spawn points	Delete handler required the wrong state condition
User can reorder animation frames via API	FastAPI route order caused reorder to be parsed as an integer ID
This showed that the evaluator was useful because it tested the app directly and gave actionable feedback.

Simplifying the harness
The first harness was powerful but slow and expensive.

Anthropic then tested whether newer models needed less scaffolding. With Opus 4.6, they removed the sprint structure and used a simpler flow:

Planner creates the spec.

Generator builds the full app.

Evaluator performs QA at the end.

Generator fixes issues.

Evaluator checks again.

This worked better because Opus 4.6 could sustain longer coding sessions without as much decomposition.

Example: Browser DAW
They tested the simplified harness with this prompt:

Build a fully featured DAW in the browser using the Web Audio API.

The run took about 3 hours 50 minutes and cost about $124.70.

Phase	Duration	Cost
Planner	4.7 min	$0.46
Build Round 1	2 hr 7 min	$71.08
QA Round 1	8.8 min	$3.24
Build Round 2	1 hr 2 min	$36.89
QA Round 2	6.8 min	$3.09
Build Round 3	10.9 min	$5.88
QA Round 3	9.6 min	$4.06
Total	3 hr 50 min	$124.70
The evaluator caught important missing features, including:

clips could not be moved on the timeline

instrument panels were missing

effect editors were too shallow

audio recording was still stubbed

clip resizing and splitting were incomplete

After multiple rounds, the final app had the core pieces of a browser-based music production tool.

Key lessons
The article’s main lessons are:

Harness design matters.
The way agents are structured can dramatically affect output quality.

Separate generation from evaluation.
A dedicated evaluator is more useful than asking the builder to judge its own work.

Use planners for vague prompts.
A planner helps turn short user requests into ambitious, structured product specs.

Decompose when needed.
Older or weaker models benefit from sprints and context resets.

Simplify as models improve.
Newer models may need less scaffolding, so harnesses should be re-tested over time.

Evaluators are most useful at the frontier.
QA agents matter most when the task is beyond what the generator can reliably do alone.

Short summary
Anthropic found that long-running coding agents work better when placed inside a structured harness. The strongest pattern uses a planner, generator, and evaluator. The planner creates a spec, the generator builds the app, and the evaluator tests it critically. This approach is slower and more expensive than a solo agent, but it produces much better results on complex applications.


