# Claude Agents Orchestration Template

A template repository for configuring Claude's agent fleet and custom commands across different projects.

## Quick Start

1. **Use this template** to create a new repository for your project
2. **Configure your agents** in `.claude/agents/`
3. **Add custom commands** in `.claude/commands/`
4. **Customize settings** in `.claude/settings.json`
5. **Update CLAUDE.md** with project-specific instructions


Expected output
```shell
╭───────────────────────────────────────────────────╮
│ ✻ Welcome to Claude Code!                         │
│                                                   │
│   /help for help, /status for your current setup  │
│                                                   │
│   cwd: /projects/claude-agents-fleet              │
╰───────────────────────────────────────────────────╯

> who are you

● I am Claude Code, an AI assistant specialized in software engineering tasks. I'm currently configured as an agent
  orchestrator for your project, which means my role is to coordinate work between different specialized agents to achieve
  complex goals.

  Based on your CLAUDE.md configuration, I:
  - Delegate tasks to specialist agents rather than implementing directly
  - Maintain project context and progress in markdown files
  - Coordinate multi-step workflows through checkpoints
  - Ensure quality through systematic review processes

  I can help you with software development tasks by orchestrating various specialist agents like solution architects,
  developers, testers, and reviewers to complete your requirements efficiently.

╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ >                                                                                                                              │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
  ? for shortcuts
```

## Repository Structure

```
.claude/
├── agents/                       # Specialized agent definitions
│   ├── solution-architect.md    # Plans and designs solutions
│   ├── senior-dev-implementer.md # Implements features and fixes
│   ├── test-quality-engineer.md # Ensures code quality
│   └── ...                      # Add your custom agents
├── commands/                     # Custom Claude commands
│   ├── planwork.md              # Plan-based implementation
│   └── colab.md                 # Collaborative workflow
└── settings.json                # Claude configuration

CLAUDE.md                        # Project-specific instructions
specs/                          # Requirements and specifications
```

## Core Components

### Agents
Pre-configured specialist agents for different tasks:
- **solution-architect** - Creates implementation plans
- **senior-dev-implementer** - Writes and refactors code
- **test-quality-engineer** - Runs tests and ensures quality
- **requirement-reviewer** - Validates implementations
- **release-version-manager** - Manages releases
- **tech-stack-auditor** - Audits dependencies
- **tech-stack-maintainer** - Updates dependencies

### Commands
Custom commands to streamline workflows:
- `/planwork` - Structured implementation with planning
- `/colab` - Collaborative development workflow

### CLAUDE.md
The main instruction file that defines how Claude should behave in your project. Update this with your project-specific guidelines.

## Customization

### Adding New Agents
1. Create a new `.md` file in `.claude/agents/`
2. Define the agent's role and capabilities
3. Claude will automatically discover and use it

### Creating Commands
1. Add a new `.md` file in `.claude/commands/`
2. Define the command behavior and workflow
3. Use `/your-command` in Claude to execute

### Project Settings
Edit `.claude/settings.json` to configure:
- Tool permissions
- Agent behaviors
- Project-specific settings

## Usage Patterns

### For New Features
1. Place requirements in `specs/` folder. Ex: `specs/login/requirement.md`
2. Use `/planwork` to let Claude implement the feature. Ex: `/planwork specs/login/`
3. Claude will coordinates agents to complete the work. This entire plan can be run in several hours to finish the work completely.

### For adhocs work
- Use `/colab` to tell Claude coordinate work instead of doing it as a standalone process. Ex: `/colab can you rename the variable array to list`.

## Benefit

You can get entire features, refactor or bugfix done autonomously, without getting your hand dirty. Leaving your valuable time to do the real engineering work.  

## Additional Resources

### Community Agents
For more specialized programming agents, check out the community-maintained collection:
- 🔗 [Awesome Claude Code Subagents](https://github.com/VoltAgent/awesome-claude-code-subagents)
  - Frontend development agents
  - Backend specialists
  - DevOps and infrastructure agents
  - Language-specific experts
  - And many more...

Simply download the agents you need and add them to your `.claude/agents/` directory.

## Getting Started

1. Clone this template
2. Review existing agents and commands
3. Browse [community agents](https://github.com/VoltAgent/awesome-claude-code-subagents) for additional capabilities
4. Customize CLAUDE.md for your project
5. Start using Claude with your configured agents

---

Built for teams using [Claude Code](https://claude.ai/code) to streamline development workflows.
