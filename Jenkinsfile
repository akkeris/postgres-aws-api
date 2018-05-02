node {
    stage "pull dockerfiles"
    git branch: 'master', credentialsId: 'gitlab', url: 'https://github.com/akkeris/postgres-rds-api'
    
    registry_url    = "https://docker.io"
    docker_creds_id = "akkeris-ops"
    org_name        = "akkeris"

    stage "build image"

    docker.withRegistry("${registry_url}", "${docker_creds_id}") {
        build_tag = "1.0.${env.BUILD_NUMBER}"
        container_name = "postgres-rds-api"
        container = docker.build("${org_name}/${container_name}:${build_tag}")
        
        container.push()
        container.push 'latest'
    }

}
