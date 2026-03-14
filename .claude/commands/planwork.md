---
description: write code to implement the task described in a spec file
allowed-tools: Bash(find:*),Bash(ls *),Write
arguments:
- specfile: the name or the path name to the spec file describe what need to be implemented
---

## Context

## Your task
- coordinate the work between multiple agent to plan and execute the requirement in {specfile}.
    - Check for the `context.md` and `progress.md` in the same {specfile} folder. If those files are existing. Load it to memory and continue the plan in the `progress.md`
    - If not existed, Assign to agent `solution-architect-planner `to research and plan the work. Create the plan as `context.md` and `progress.md`.
    - Assign the work to right agent and execute the plan create by agent `solution-architect-planner`. Execute task in the same checkpoint paralelly if possible.
    - After finishing each checkpoint, stop and ask for human feedback.
    - When finish all check point, review the work.

