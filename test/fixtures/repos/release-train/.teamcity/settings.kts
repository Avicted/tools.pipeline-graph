import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.triggers.schedule

version = "2024.03"

project {
    val chain = sequential {
        buildType(Smoke)
        parallel {
            buildType(AbsoluteId("ServiceA_Deploy_staging"))
            buildType(AbsoluteId("Billing_Deploy_staging"))
        }
        buildType(Release)
    }
    chain.buildTypes().forEach { buildType(it) }
}

object Smoke : BuildType({
    name = "Nightly smoke"
    steps {
        script { scriptContent = "make smoke"; workingDir = "." }
    }
    triggers {
        schedule {
            schedulingPolicy = daily {
                hour = 2
            }
            branchFilter = "+:<default>"
        }
    }
})

object Release : BuildType({
    name = "Release train"
    type = BuildTypeSettings.Type.COMPOSITE
})
