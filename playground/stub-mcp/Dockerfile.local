FROM python:3.12-slim
WORKDIR /app
COPY playground/stub-mcp/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY playground/stub-mcp/main.py .
EXPOSE 8090
CMD ["python", "main.py"]
