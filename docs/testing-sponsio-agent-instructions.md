# Instructions for LLM Agents: Verifying Sponsio Setup

**Goal**: Agnostically verify that Sponsio is installed, wired up, and actively functioning on your current target project, regardless of the specific contracts inside `sponsio.yaml`.

**CRITICAL CONTEXT & WARNING**: You are reading this file from a central repository. **DO NOT INSTALL OR RUN THESE COMMANDS IN THIS REPOSITORY.** 
You must **ONLY** run these diagnostic and installation commands in the **ROOT DIRECTORY OF YOUR OWN TARGET PROJECT/REPO**. Running them here will corrupt the central configuration and is strictly forbidden.

Do NOT attempt to guess Sponsio's status by just reading files. You must empirically prove it is functioning by executing the following diagnostic steps.

## Step 1: Verify Installation
Run the following command in your terminal to ensure the Sponsio CLI is available in your active environment path:
```bash
sponsio --version
```
If this fails, Sponsio is not installed or not in your PATH.

## Step 2: Run the Universal Smoke Test (Crucial)
Sponsio includes a built-in diagnostic tool that actively tests the runtime hooks and guard wiring without relying on project-specific rules. From the root of your target project, run:
```bash
sponsio doctor
```

Examine the output carefully. You must verify that:
1. **Sponsio import** succeeds.
2. **Config file** successfully locates the `sponsio.yaml` in your project and detects the correct number of agents.
3. **Guard smoke-test** passes (Look for: `✓ Guard smoke-test  contract wires up...`). **This specifically proves the internal contract engine is alive and actively evaluates inputs.**

If `sponsio doctor` reports failures (not just warnings), Sponsio is NOT functioning correctly on your project. Stop and troubleshoot the installation before proceeding.

## Step 3: Validate Project Configuration
Agnostically ensure your project's specific Sponsio configuration is syntactically valid and loads successfully into the guard engine. Ensure you are in your project's root directory and run:
```bash
sponsio validate --config sponsio.yaml
```
If the contracts compile and validate successfully, the setup is secure.

**Conclusion**: If all three commands exit cleanly and the `sponsio doctor` smoke-test passes, you have empirically proven that Sponsio is installed, properly configured, and actively functioning on your target project.
