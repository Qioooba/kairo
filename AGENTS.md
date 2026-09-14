# 仓库开发与协同规范

## Git Commit 提交约束 (强制)

在本项目中执行 `git commit` 时，必须严格遵守以下规范：

1. **必须注明模型版本**：每次提交信息（Commit Message）末尾（Footer / Trailer）必须明确附带实际执行提交的 **AI 模型全称及精确版本号**。
   - 格式：
     ```text
     Model: <精确模型全称与版本>
     ```
   - 示例：
     - `Model: Gemini 3.8 Flash`
     - `Model: Gemini 3.8 Pro`
     - `Model: Claude 3.7 Sonnet`
     - `Model: GPT-4o`
2. **严禁使用模糊泛称**：禁止只填写 `Model: Gemini`、`Model: AI`、`Model: LLM` 等没有版本号的名称，必须精确到具体版本。
3. **准确感知当前模型**：提交前必须确认当前实际运行的模型版本（如 `Gemini 3.8 Flash`），如实写入。
4. **标准提交格式模板**：
   ```text
   <type>(<scope>): <简要总结>

   - 变更点说明 1
   - 变更点说明 2

   Model: <当前运行的精确模型名称与版本号>
   ```
